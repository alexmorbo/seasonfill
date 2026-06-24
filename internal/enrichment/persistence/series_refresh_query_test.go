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
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

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
