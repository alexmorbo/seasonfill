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

// seedMovieI18n inserts one movie_i18n row for the given movie/language. A nil
// overview leaves the column NULL (the "TMDB had no translation" state); a
// non-nil pointer to "" seeds an empty-string overview (the "row exists but blank"
// state). Both are treated as MISSING coverage by the S3 picker predicate.
func seedMovieI18n(t *testing.T, db *gorm.DB, id domain.MovieID, lang string, overview *string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.MovieI18nModel{
		MovieID:    id,
		Language:   lang,
		Overview:   overview,
		EnrichedAt: &now,
		UpdatedAt:  now,
	}).Error)
}

// seedMovieI18nFull seeds a movie_i18n row with explicit title + overview (nil → NULL).
func seedMovieI18nFull(t *testing.T, db *gorm.DB, id domain.MovieID, lang string, title, overview *string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.MovieI18nModel{
		MovieID: id, Language: lang, Title: title, Overview: overview,
		EnrichedAt: &now, UpdatedAt: now,
	}).Error)
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
			// S3: both are text-attempted (enrichment_text_synced_at non-NULL) so the new
			// i18n-coverage OR does NOT re-pick them — they stay excluded on their
			// tmdb-path grounds (race guard / TTL), isolating this test to those grounds.
			require.NoError(t, repo.MarkTextSynced(ctx, rg, recent))
			require.NoError(t, repo.MarkTextSynced(ctx, nf, now))

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

			// S3: both excluded fixtures are text-attempted, so the new i18n-coverage OR
			// does not re-pick them — this test stays scoped to the NULL-section predicate.
			require.NoError(t, repo.MarkTextSynced(ctx, fullStamped, fresh))
			require.NoError(t, repo.MarkTextSynced(ctx, emptyStamped, fresh))

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

// TestMovieRepository_PickMovieRefreshCandidates_I18nCoverage covers S3: the
// NORMAL-arm i18n-coverage branch re-picks a movie missing a non-empty non-base
// (ru-RU) overview, guarded by enrichment_text_synced_at IS NULL so it fires at
// most once (anti-storm). All movies here are tmdb-fresh with all 4 section stamps
// non-NULL, so ONLY the i18n branch can pull them into the NORMAL tier — isolating
// the predicate under test.
func TestMovieRepository_PickMovieRefreshCandidates_I18nCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
			fresh := now.Add(-1 * time.Hour) // within Normal TTL (14d), no change flag
			ttl := enrichment.DefaultRefreshTTL()

			// Every movie is tmdb-fresh + all 4 sections stamped, so the tmdb/section
			// ORs are all false — only the S3 i18n branch can pick a movie here.
			allSectionsFresh := func(id domain.MovieID) {
				markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh))
			}

			// (a) text NULL + NO ru-RU overview row → PICKED (missing coverage).
			//     The movie-558449 hole: enriched before the S3 text writer existed.
			noRu := seedMovie(t, db, 400, new(fresh), nil)
			allSectionsFresh(noRu)

			// (b) text NULL + ru-RU title AND overview present (non-empty) → NOT picked
			//     (fully covered). The title must be seeded too now that the U-1b title
			//     branch also re-picks on a missing non-empty ru title.
			hasRu := seedMovie(t, db, 401, new(fresh), nil)
			allSectionsFresh(hasRu)
			seedMovieI18nFull(t, db, hasRu, "ru-RU", new("Название"), new("русское описание"))

			// (c) text STAMPED (non-NULL) + still no ru overview → NOT picked.
			//     THE anti-storm case: TMDB genuinely has no ru, the worker stamped
			//     text-synced on its one attempt, so the movie is never re-picked.
			stampedNoRu := seedMovie(t, db, 402, new(fresh), nil)
			allSectionsFresh(stampedNoRu)
			require.NoError(t, repo.MarkTextSynced(ctx, stampedNoRu, fresh))

			// (d) text NULL + ru-RU row exists but overview = '' (empty) → PICKED
			//     (empty string treated as missing, same as no row).
			emptyRu := seedMovie(t, db, 403, new(fresh), nil)
			allSectionsFresh(emptyRu)
			seedMovieI18n(t, db, emptyRu, "ru-RU", new(""))

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)

			ids := map[domain.MovieID]enrichment.RefreshTier{}
			for _, c := range got {
				ids[c.MovieID] = c.Tier
			}

			// (a) picked, NORMAL tier.
			tier, ok := ids[noRu]
			require.True(t, ok, "movie with no ru overview + text NULL must be re-picked; got %+v", got)
			assert.Equal(t, enrichment.RefreshTierNormal, tier)

			// (b) covered → absent.
			_, ok = ids[hasRu]
			assert.False(t, ok, "movie with a non-empty ru overview must NOT be picked")

			// (c) anti-storm: stamped + no ru → absent.
			_, ok = ids[stampedNoRu]
			assert.False(t, ok, "text-stamped movie must NOT be re-picked even with no ru (anti-storm)")

			// (d) empty-string overview → treated as missing → picked.
			tier, ok = ids[emptyRu]
			require.True(t, ok, "movie with an empty ru overview must be re-picked; got %+v", got)
			assert.Equal(t, enrichment.RefreshTierNormal, tier)

			// Exactly the two uncovered movies are pickable.
			require.Len(t, got, 2, "only noRu + emptyRu are pickable; got %+v", got)
		})
	}
}

func TestMovieRepository_PickMovieRefreshCandidates_TitleCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewMovieRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
			fresh := now.Add(-1 * time.Hour)
			ttl := enrichment.DefaultRefreshTTL()
			s := func(v string) *string { return &v }
			allFresh := func(id domain.MovieID) { markMovieSections(t, db, id, new(fresh), new(fresh), new(fresh), new(fresh)) }

			// (a) THE 10,806: text NULL, ru overview present, ru TITLE empty → PICKED.
			emptyTitle := seedMovie(t, db, 600, new(fresh), nil)
			allFresh(emptyTitle)
			seedMovieI18nFull(t, db, emptyTitle, "ru-RU", nil, s("русское описание"))

			// (b) text NULL, ru title AND overview present → NOT picked (fully covered).
			covered := seedMovie(t, db, 601, new(fresh), nil)
			allFresh(covered)
			seedMovieI18nFull(t, db, covered, "ru-RU", s("Название"), s("описание"))

			// (c) ANTI-STORM: text SET, ru title empty, overview present → NOT picked forever.
			stamped := seedMovie(t, db, 602, new(fresh), nil)
			allFresh(stamped)
			seedMovieI18nFull(t, db, stamped, "ru-RU", nil, s("описание"))
			require.NoError(t, repo.MarkTextSynced(ctx, stamped, fresh))

			// (d) text NULL, ru title = '' (empty string) → PICKED (empty treated as missing).
			emptyStr := seedMovie(t, db, 603, new(fresh), nil)
			allFresh(emptyStr)
			seedMovieI18nFull(t, db, emptyStr, "ru-RU", s(""), s("описание"))

			got, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 50)
			require.NoError(t, err)
			ids := map[domain.MovieID]enrichment.RefreshTier{}
			for _, c := range got {
				ids[c.MovieID] = c.Tier
			}

			_, ok := ids[emptyTitle]
			assert.True(t, ok, "empty-title + text NULL must be re-picked; got %+v", got)
			_, ok = ids[covered]
			assert.False(t, ok, "fully-covered movie must NOT be picked")
			_, ok = ids[stamped]
			assert.False(t, ok, "text-stamped movie must NOT be re-picked (anti-storm)")
			_, ok = ids[emptyStr]
			assert.True(t, ok, "empty-string title must be re-picked")

			require.Len(t, got, 2, "only emptyTitle + emptyStr pickable; got %+v", got)
		})
	}
}
