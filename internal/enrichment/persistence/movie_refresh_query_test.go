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
			_ = seedMovie(t, db, 102, new(recent), new(now.Add(-1*time.Hour)))
			// normalStale: no change flag, sync old → NORMAL tier.
			normalStale := seedMovie(t, db, 200, new(old), nil)
			// normalFresh: no change flag, synced just now → EXCLUDED (within TTL).
			_ = seedMovie(t, db, 201, new(now), nil)

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
