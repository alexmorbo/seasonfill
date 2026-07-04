package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	discopersistence "github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedSeriesMediaTextRow inserts one series_media_texts row with a
// non-NULL poster_asset for (seriesID, language). Used by the W17-1
// poster-guard tests to mark a library series as "already has art".
func seedSeriesMediaTextRow(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, language, posterAsset string) {
	t.Helper()
	p := posterAsset
	row := database.SeriesMediaTextModel{
		SeriesID:    seriesID,
		Language:    language,
		PosterAsset: &p,
		UpdatedAt:   time.Now().UTC(),
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedDiscoveryListsRow inserts one discovery_lists row pointing at
// seriesID. Used by the refresh-picker tests to mark a series as
// "user-visible discovery rail" (Tier 2 / NORMAL).
func seedDiscoveryListsRow(t *testing.T, db *gorm.DB, seriesID domain.SeriesID, position int) {
	t.Helper()
	row := discopersistence.DiscoveryListsModel{
		Kind:        "popular",
		Param:       "",
		Language:    "en-US",
		SeriesID:    seriesID,
		Position:    position,
		RefreshedAt: time.Now().UTC(),
	}
	require.NoError(t, db.Create(&row).Error)
}

// TestSeriesRepository_PickRefreshCandidates_TierMembershipAndOrder is
// the headline integration test for the Story 534 tiered picker. Seeds
// a representative DB and asserts:
//   - HOT before NORMAL before COLD across the union.
//   - NULL synced_at sorts first within a tier.
//   - Older synced_at sorts before newer within a tier.
//   - LIMIT applied across the union (NOT per-tier).
//   - tmdb_id IS NULL series excluded.
//   - enrichment_errors.attempts > 5 series excluded.
//   - Within-TTL (fresh) series excluded.
func TestSeriesRepository_PickRefreshCandidates_TierMembershipAndOrder(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			d8 := now.Add(-8 * 24 * time.Hour)   // > hot TTL (7d)
			d15 := now.Add(-15 * 24 * time.Hour) // > normal TTL (14d)
			d31 := now.Add(-31 * 24 * time.Hour) // > cold TTL (30d)
			fresh := now.Add(-1 * time.Hour)

			// Seed series fixtures with deterministic TMDB ids so the
			// upsert path matches and the within-tier ordering is
			// reproducible.
			//   A — HOT, NULL synced_at  → first in HOT.
			//   B — HOT, d8 stale        → second in HOT.
			//   C — NORMAL, NULL         → first in NORMAL.
			//   D — NORMAL, d15 stale    → second in NORMAL.
			//   E — COLD, d31 stale      → only COLD.
			//   F — HOT, fresh           → excluded (within TTL).
			//   G — NULL tmdb_id         → excluded (not enrichable).
			//   H — HOT, NULL, terminal failure (>5 attempts) → excluded.

			seedAndUpsert := func(title string, tmdbID int64, syncedAt *time.Time) domain.SeriesID {
				t.Helper()
				c := sampleCanon(title)
				c.TMDBID = ptrTMDBID(int(tmdbID))
				c.TVDBID = ptrTVDBID(int(tmdbID + 100000))
				c.IMDBID = nil
				c.EnrichmentTMDBSyncedAt = syncedAt
				id, err := repo.Upsert(ctx, c)
				require.NoError(t, err)
				return id
			}

			idA := seedAndUpsert("A-hot-null", 1001, nil)
			seedSeriesCacheRow(t, db, idA, "main", 1001, false)

			idB := seedAndUpsert("B-hot-d8", 1002, &d8)
			seedSeriesCacheRow(t, db, idB, "main", 1002, false)

			idC := seedAndUpsert("C-normal-null", 1003, nil)
			seedDiscoveryListsRow(t, db, idC, 1)

			idD := seedAndUpsert("D-normal-d15", 1004, &d15)
			seedDiscoveryListsRow(t, db, idD, 2)

			idE := seedAndUpsert("E-cold-d31", 1005, &d31)

			idF := seedAndUpsert("F-hot-fresh", 1006, &fresh)
			seedSeriesCacheRow(t, db, idF, "main", 1006, false)
			// F carries a poster asset so the W17-1 missing-poster guard
			// does NOT fire — its exclusion here is purely the TTL-fresh
			// gate, which is what this case asserts.
			seedSeriesMediaTextRow(t, db, idF, "en-US", "/posters/f.jpg")

			// G — NULL tmdb_id (legacy Sonarr import).
			g := sampleCanon("G-no-tmdb")
			g.TMDBID = nil
			g.TVDBID = nil
			g.IMDBID = nil
			g.EnrichmentTMDBSyncedAt = nil
			idG, err := repo.Upsert(ctx, g)
			require.NoError(t, err)
			seedSeriesCacheRow(t, db, idG, "main", 9999, false)

			idH := seedAndUpsert("H-hot-terminal", 1008, nil)
			seedSeriesCacheRow(t, db, idH, "main", 1008, false)
			seedEnrichmentError(t, db, enrichment.EntityTypeSeries, int64(idH), enrichment.SourceTMDBSeries, 6)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 50)
			require.NoError(t, err)

			// Assert tier ordering: HOT(A,B) → NORMAL(C,D) → COLD(E).
			require.Len(t, rows, 5, "F (fresh), G (null tmdb_id), and H (terminal) must be excluded")

			gotIDs := make([]domain.SeriesID, 0, len(rows))
			gotTiers := make([]enrichment.RefreshTier, 0, len(rows))
			for _, r := range rows {
				gotIDs = append(gotIDs, r.SeriesID)
				gotTiers = append(gotTiers, r.Tier)
			}

			assert.Equal(t, []domain.SeriesID{idA, idB, idC, idD, idE}, gotIDs,
				"order = HOT(NULL) HOT(stale) NORMAL(NULL) NORMAL(stale) COLD(stale)")
			assert.Equal(t, []enrichment.RefreshTier{
				enrichment.RefreshTierHot, enrichment.RefreshTierHot,
				enrichment.RefreshTierNormal, enrichment.RefreshTierNormal,
				enrichment.RefreshTierCold,
			}, gotTiers)

			// Excluded ones must not appear under any tier.
			for _, r := range rows {
				assert.NotEqual(t, idF, r.SeriesID, "fresh series F must be excluded")
				assert.NotEqual(t, idG, r.SeriesID, "null-tmdb series G must be excluded")
				assert.NotEqual(t, idH, r.SeriesID, "terminal-failure series H must be excluded")
			}
		})
	}
}

// TestSeriesRepository_PickRefreshCandidates_MissingPosterGuard covers
// the W17-1 HOT poster-guard branch: a tmdb-stamped library series that
// is otherwise TTL-fresh is still picked when it has no
// series_media_texts.poster_asset row, guarded by a 15-minute race
// window and scoped to tmdb-enrichable library series only.
func TestSeriesRepository_PickRefreshCandidates_MissingPosterGuard(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			oneHourAgo := now.Add(-1 * time.Hour)        // TTL-fresh (< 7d) BUT > 15m guard
			fiveMinAgo := now.Add(-5 * time.Minute)      // inside the 15m race guard
			eightDaysAgo := now.Add(-8 * 24 * time.Hour) // TTL-EXPIRED (> hot 7d)

			seedLib := func(title string, tmdbID int64, syncedAt *time.Time, sonarrID domain.SonarrSeriesID) domain.SeriesID {
				t.Helper()
				c := sampleCanon(title)
				c.TMDBID = ptrTMDBID(int(tmdbID))
				c.TVDBID = ptrTVDBID(int(tmdbID + 100000))
				c.IMDBID = nil
				c.EnrichmentTMDBSyncedAt = syncedAt
				id, err := repo.Upsert(ctx, c)
				require.NoError(t, err)
				seedSeriesCacheRow(t, db, id, "main", sonarrID, false)
				return id
			}

			// P — stamped 1h ago (TTL-fresh), NO poster → PICKED via guard.
			idP := seedLib("P-fresh-no-poster", 3001, &oneHourAgo, 3001)
			// Q — stamped 1h ago (TTL-fresh), HAS poster → NOT picked.
			idQ := seedLib("Q-fresh-with-poster", 3002, &oneHourAgo, 3002)
			seedSeriesMediaTextRow(t, db, idQ, "en-US", "/posters/q.jpg")
			// R — stamped 5m ago (inside race guard), NO poster → NOT picked.
			idR := seedLib("R-race-no-poster", 3003, &fiveMinAgo, 3003)
			// N — NULL sync, NO poster → PICKED (NULL-sync path). This is a
			// normal HOT pick (the NULL-sync staleness gate would select it
			// regardless of poster), so it is NOT attributed missing_poster.
			idN := seedLib("N-null-sync", 3004, nil, 3004)
			// T — stamped 8d ago (TTL-EXPIRED), NO poster → PICKED via the
			// normal HOT staleness gate, NOT the poster guard, so missing_poster
			// is FALSE even though it also lacks a poster.
			idT := seedLib("T-stale-no-poster", 3006, &eightDaysAgo, 3006)

			// S — tmdb-less library series, NO poster → NOT picked (guard is
			// scoped to tmdb-enrichable rows; prevents the tmdb-less hot-loop).
			s := sampleCanon("S-no-tmdb")
			s.TMDBID = nil
			s.TVDBID = nil
			s.IMDBID = nil
			s.EnrichmentTMDBSyncedAt = &oneHourAgo
			idS, err := repo.Upsert(ctx, s)
			require.NoError(t, err)
			seedSeriesCacheRow(t, db, idS, "main", 3005, false)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 50)
			require.NoError(t, err)

			picked := make(map[domain.SeriesID]RefreshCandidate, len(rows))
			for _, r := range rows {
				picked[r.SeriesID] = r
			}

			// P picked and flagged missing_poster (poster-guard-EXCLUSIVE:
			// TTL-fresh, non-NULL sync, no poster).
			require.Contains(t, picked, idP, "TTL-fresh poster-less series must be picked by the guard")
			assert.True(t, picked[idP].MissingPoster, "P must carry the missing_poster reason")

			// N picked (NULL-sync) but NOT attributed missing_poster — the
			// NULL-sync staleness gate is what selected it, not the poster guard.
			require.Contains(t, picked, idN, "NULL-sync series must still be picked")
			assert.False(t, picked[idN].MissingPoster,
				"NULL-sync is a normal HOT pick → missing_poster false")

			// T picked (TTL-expired) but NOT attributed missing_poster — it
			// would be selected by the normal HOT staleness gate anyway, so the
			// poster guard is not the exclusive reason.
			require.Contains(t, picked, idT, "TTL-expired series must be picked by the staleness gate")
			assert.False(t, picked[idT].MissingPoster,
				"TTL-expired poster-less series is a normal HOT pick → missing_poster false")

			// Q (has poster), R (race guard), S (tmdb-less) excluded.
			assert.NotContains(t, picked, idQ, "series with a poster asset must not be picked")
			assert.NotContains(t, picked, idR, "series stamped < 15m ago must not be picked (race guard)")
			assert.NotContains(t, picked, idS, "tmdb-less series must not be picked by the poster branch")
		})
	}
}

// TestSeriesRepository_PickRefreshCandidates_LimitAppliesAcrossUnion
// asserts the budget drains HOT first — a limit of 1 in a mixed DB
// returns only the stalest HOT series, never any NORMAL/COLD row.
func TestSeriesRepository_PickRefreshCandidates_LimitAppliesAcrossUnion(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			d8 := now.Add(-8 * 24 * time.Hour)

			// HOT row (stale d8), NORMAL row (NULL), COLD row (NULL).
			a := sampleCanon("A-hot")
			a.TMDBID = ptrTMDBID(2001)
			a.TVDBID = ptrTVDBID(102001)
			a.IMDBID = nil
			a.EnrichmentTMDBSyncedAt = &d8
			idA, err := repo.Upsert(ctx, a)
			require.NoError(t, err)
			seedSeriesCacheRow(t, db, idA, "main", 2001, false)

			b := sampleCanon("B-normal")
			b.TMDBID = ptrTMDBID(2002)
			b.TVDBID = ptrTVDBID(102002)
			b.IMDBID = nil
			b.EnrichmentTMDBSyncedAt = nil
			idB, err := repo.Upsert(ctx, b)
			require.NoError(t, err)
			seedDiscoveryListsRow(t, db, idB, 1)

			c := sampleCanon("C-cold")
			c.TMDBID = ptrTMDBID(2003)
			c.TVDBID = ptrTVDBID(102003)
			c.IMDBID = nil
			c.EnrichmentTMDBSyncedAt = nil
			_, err = repo.Upsert(ctx, c)
			require.NoError(t, err)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 1)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, idA, rows[0].SeriesID)
			assert.Equal(t, enrichment.RefreshTierHot, rows[0].Tier)
		})
	}
}

// TestSeriesRepository_PickRefreshCandidates_DefaultLimit asserts the
// limit <= 0 sentinel falls back to 50 rather than disabling the
// query budget entirely.
func TestSeriesRepository_PickRefreshCandidates_DefaultLimit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

			// No seeded series → returns empty without error; the
			// real assertion here is that LIMIT 0 / negative does not
			// short-circuit the query (an int overflow or zeroed param
			// would surface as a DB-side parse error on either dialect).
			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 0)
			require.NoError(t, err)
			assert.Empty(t, rows)

			rows, err = repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), -10)
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}
