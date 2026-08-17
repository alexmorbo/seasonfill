package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedMovie inserts a stub movie with the given tmdb_id, then (optionally)
// stamps enrichment_tmdb_synced_at / tmdb_changed_at directly. A nil pointer
// leaves the column NULL. Returns the assigned movie id. tmdb_changed_at is set
// via UpdateColumns (the marker's grep-AC column) because Upsert never writes it.
func seedMovie(t *testing.T, db *gorm.DB, tmdbID int, syncedAt, changedAt *time.Time) domain.MovieID {
	t.Helper()
	repo := NewMovieRepository(db)
	tid := domain.TMDBID(tmdbID)
	id, err := repo.Upsert(context.Background(), movie.Canon{
		TMDBID:    &tid,
		Title:     fmt.Sprintf("m%d", tmdbID),
		Hydration: movie.HydrationStub,
	})
	require.NoError(t, err)
	updates := map[string]any{}
	if syncedAt != nil {
		updates["enrichment_tmdb_synced_at"] = syncedAt.UTC()
	}
	if changedAt != nil {
		updates["tmdb_changed_at"] = changedAt.UTC()
	}
	if len(updates) > 0 {
		require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).UpdateColumns(updates).Error)
	}
	return id
}

// markMovieSections stamps the four per-section enrichment clocks
// (enrichment_{cast,keywords,recs,media}_synced_at) on an existing movie. A nil
// pointer leaves that column NULL. Lets a test place a movie in the "fully
// processed" state (all 4 non-NULL) or reproduce the pre-Ф1 hole (fresh tmdb
// clock, one-or-more section clock NULL).
func markMovieSections(t *testing.T, db *gorm.DB, id domain.MovieID, cast, keywords, recs, media *time.Time) {
	t.Helper()
	updates := map[string]any{}
	if cast != nil {
		updates["enrichment_cast_synced_at"] = cast.UTC()
	}
	if keywords != nil {
		updates["enrichment_keywords_synced_at"] = keywords.UTC()
	}
	if recs != nil {
		updates["enrichment_recs_synced_at"] = recs.UTC()
	}
	if media != nil {
		updates["enrichment_media_synced_at"] = media.UTC()
	}
	if len(updates) == 0 {
		return
	}
	require.NoError(t, db.Model(&database.MovieModel{}).Where("id = ?", id).UpdateColumns(updates).Error)
}

// TestMovieRepository_PickMovieRefreshCandidates covers the 2-tier picker:
// CHANGED before NORMAL, NULL-sync first within a tier, the 15m race guard
// excluding a just-stamped changed movie, anti-double-pick (a changed+stale
// movie appears exactly once, in tier 0), limit, and tmdb-less exclusion.
func TestMovieRepository_PickMovieRefreshCandidates(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
			old := now.Add(-40 * 24 * time.Hour) // older than Normal TTL (14d)
			recent := now.Add(-2 * time.Minute)  // inside the 15m race guard
			ttl := enrichment.DefaultRefreshTTL()

			// changedStale: tmdb_changed_at set, sync old → CHANGED tier, race-OK.
			changedStale := seedMovie(t, db, 100, new(old), new(now.Add(-1*time.Hour)))
			// changedNeverSynced: tmdb_changed_at set, sync NULL → CHANGED, sorts first.
			changedNeverSynced := seedMovie(t, db, 101, nil, new(now.Add(-1*time.Hour)))
			// changedButRaceGuarded: changed, but synced 2m ago → EXCLUDED (mid-Handle).
			rg := seedMovie(t, db, 102, new(recent), new(now.Add(-1*time.Hour)))
			// normalStale: no change flag, sync old → NORMAL tier.
			normalStale := seedMovie(t, db, 200, new(old), nil)
			// normalFresh: no change flag, synced just now → EXCLUDED (within TTL).
			nf := seedMovie(t, db, 201, new(now), nil)
			// Ф1.4: both are fully-processed (all 4 section stamps non-NULL) so the new
			// NULL-section OR does NOT pull them into NORMAL — they must stay excluded on
			// their tmdb-path grounds (race guard / TTL) exactly as before.
			markMovieSections(t, db, rg, new(recent), new(recent), new(recent), new(recent))
			markMovieSections(t, db, nf, new(now), new(now), new(now), new(now))

			// tmdbless: a Radarr orphan (tmdb_id NULL) → NEVER picked.
			_, err := repo.Upsert(ctx, movie.Canon{Title: "orphan", Hydration: movie.HydrationStub})
			require.NoError(t, err)

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			// Expected order: CHANGED (NULL-sync first, then older sync), then NORMAL.
			require.Len(t, got, 3, "want changedNeverSynced, changedStale, normalStale; got %+v", got)
			assert.Equal(t, changedNeverSynced, got[0].MovieID)
			assert.Equal(t, enrichment.RefreshTierChanged, got[0].Tier)
			assert.Equal(t, changedStale, got[1].MovieID)
			assert.Equal(t, enrichment.RefreshTierChanged, got[1].Tier)
			assert.Equal(t, normalStale, got[2].MovieID)
			assert.Equal(t, enrichment.RefreshTierNormal, got[2].Tier)

			// Anti-double-pick: changedStale is also TTL-stale but appears ONLY once.
			seen := map[domain.MovieID]int{}
			for _, c := range got {
				seen[c.MovieID]++
			}
			assert.Equal(t, 1, seen[changedStale], "changed+stale movie must appear exactly once")

			// LIMIT applies across the union.
			lim, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 2)
			require.NoError(t, err)
			require.Len(t, lim, 2)
			assert.Equal(t, changedNeverSynced, lim[0].MovieID)
			assert.Equal(t, changedStale, lim[1].MovieID)
		})
	}
}

// TestMovieRepository_PickMovieRefreshCandidates_NullSectionBackfill covers Ф1.4:
// a movie with a FRESH enrichment_tmdb_synced_at but a NULL section stamp
// (cast/keywords/recs/media) is re-picked into the NORMAL tier so the pre-Ф1
// section holes get filled; a fully-stamped movie (all 4 non-NULL) is NOT
// re-picked; and a movie whose sections are empty-but-STAMPED is not re-picked
// (the picker keys off the stamp column, not row counts → no churn).
func TestMovieRepository_PickMovieRefreshCandidates_NullSectionBackfill(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
			fresh := now.Add(-1 * time.Hour) // well within Normal TTL (14d), no change flag
			ttl := enrichment.DefaultRefreshTTL()

			// (a) fresh tmdb clock, cast section NULL (other 3 stamped) → PICKED (NORMAL).
			//     This is the movie-558449 hole: enriched before the Ф1 cast writer.
			nullCast := seedMovie(t, db, 300, new(fresh), nil)
			markMovieSections(t, db, nullCast, nil, new(fresh), new(fresh), new(fresh))

			// (b) fully-processed: all 4 section stamps non-NULL, tmdb fresh → NOT picked.
			fullStamped := seedMovie(t, db, 301, new(fresh), nil)
			markMovieSections(t, db, fullStamped, new(fresh), new(fresh), new(fresh), new(fresh))

			// (c) empty-section-but-STAMPED: all 4 stamped, zero child rows seeded
			//     (mirrors the worker's "checked, empty" stamp-only tx) → NOT re-picked.
			//     Proves the picker keys off the stamp column, not section row counts.
			emptyStamped := seedMovie(t, db, 302, new(fresh), nil)
			markMovieSections(t, db, emptyStamped, new(fresh), new(fresh), new(fresh), new(fresh))

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			ids := map[domain.MovieID]enrichment.RefreshTier{}
			for _, c := range got {
				ids[c.MovieID] = c.Tier
			}

			// (a) picked, in NORMAL tier.
			tier, ok := ids[nullCast]
			require.True(t, ok, "movie with NULL cast stamp must be re-picked; got %+v", got)
			assert.Equal(t, enrichment.RefreshTierNormal, tier)

			// (b) fully-stamped, tmdb-fresh → absent.
			_, ok = ids[fullStamped]
			assert.False(t, ok, "fully-stamped fresh movie must NOT be picked (no churn)")

			// (c) empty-but-stamped → absent (idempotent, no re-pick storm).
			_, ok = ids[emptyStamped]
			assert.False(t, ok, "empty-but-stamped movie must NOT be re-picked")

			// Negative-space guard: with only these 3 seeded, exactly one is pickable.
			require.Len(t, got, 1, "only the NULL-section movie is pickable; got %+v", got)
		})
	}
}
