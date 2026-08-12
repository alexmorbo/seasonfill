package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// enrichTestUserID owns the followed_series rows (Ф8-U-5 per-user FK).
// seedEnrichUser inserts the matching users row so the FK holds. followed_series
// is global-union here (the refresh/orphan queries never filter by user_id), so
// a single owner is sufficient.
const enrichTestUserID int64 = 1

func seedEnrichUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(enrichTestUserID),
		Username:   "admin",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

// seedFollowedSeriesRow inserts one followed_series row (ADR-0015 Ф3 C1).
// The series(id) FK must already exist (upsert canon first) and the owner user
// must already be seeded (seedEnrichUser).
func seedFollowedSeriesRow(t *testing.T, db *gorm.DB, seriesID domain.SeriesID) {
	t.Helper()
	row := database.FollowedSeriesModel{UserID: enrichTestUserID, SeriesID: int64(seriesID), CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&row).Error)
}

// seedEnrichUserID inserts an extra users row (the package's seedEnrichUser
// only seeds id=1). Needed so the followed_series (user_id) FK holds for a
// second follower in the global-union guard test.
func seedEnrichUserID(t *testing.T, db *gorm.DB, id int64, username string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         uint(id),
		Username:   username,
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

// seedFollowedSeriesRowForUser inserts one followed_series row for an arbitrary
// owner (seedFollowedSeriesRow hardcodes enrichTestUserID=1).
func seedFollowedSeriesRowForUser(t *testing.T, db *gorm.DB, userID int64, seriesID domain.SeriesID) {
	t.Helper()
	row := database.FollowedSeriesModel{UserID: userID, SeriesID: int64(seriesID), CreatedAt: time.Now().UTC()}
	require.NoError(t, db.Create(&row).Error)
}

// TestSeriesRepository_PickRefreshCandidates_FollowedTier_GlobalUnion proves
// that when TWO distinct users follow the SAME stale series, the FOLLOWED-tier
// picker returns it EXACTLY ONCE (shared TMDB metadata refreshed once, not per
// follower). Regression guard for Ф8-U-5: the followed_series EXISTS subqueries
// must stay unscoped (no fs.user_id predicate).
func TestSeriesRepository_PickRefreshCandidates_FollowedTier_GlobalUnion(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			d11 := now.Add(-11 * 24 * time.Hour) // > followed TTL (10d) → stale
			seedEnrichUser(t, db)                // id=1 (admin)
			seedEnrichUserID(t, db, 2, "user2")  // id=2 (non-admin follower)

			c := sampleCanon("shared-followed")
			c.TMDBID = ptrTMDBID(4201)
			c.TVDBID = ptrTVDBID(104201)
			c.IMDBID = nil
			c.EnrichmentTMDBSyncedAt = &d11
			sID, err := repo.Upsert(ctx, c)
			require.NoError(t, err)

			// BOTH users follow the same series.
			seedFollowedSeriesRowForUser(t, db, 1, sID)
			seedFollowedSeriesRowForUser(t, db, 2, sID)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 50)
			require.NoError(t, err)

			occurrences := 0
			var tier enrichment.RefreshTier
			for _, r := range rows {
				if r.SeriesID == sID {
					occurrences++
					tier = r.Tier
				}
			}
			require.Equal(t, 1, occurrences,
				"two users following the same series must yield exactly ONE FOLLOWED candidate (global union)")
			assert.Equal(t, enrichment.RefreshTierFollowed, tier)
		})
	}
}

// TestSeriesRepository_PickRefreshCandidates_FollowedTier covers the
// ADR-0015 Ф3 C1 FOLLOWED tier (2): a followed-not-in-library series lands
// on the Followed tier (10d TTL), never decays to Cold (F-04); Hot wins for
// followed+in-library; Followed wins over Normal for followed+in-discovery;
// and the union never double-picks a followed series.
func TestSeriesRepository_PickRefreshCandidates_FollowedTier(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			d11 := now.Add(-11 * 24 * time.Hour) // > followed TTL (10d), < normal (14d)
			seedEnrichUser(t, db)

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

			// A — followed, NOT in library, stale 11d → FOLLOWED (2), not Cold.
			idA := seedAndUpsert("A-followed-not-lib", 4001, &d11)
			seedFollowedSeriesRow(t, db, idA)

			// B — followed AND in library → HOT wins.
			idB := seedAndUpsert("B-followed-in-lib", 4002, &d11)
			seedFollowedSeriesRow(t, db, idB)
			seedSeriesCacheRow(t, db, idB, "main", 4002, false)

			// C — followed AND in discovery_lists (not in cache) → FOLLOWED wins.
			idC := seedAndUpsert("C-followed-in-disco", 4003, &d11)
			seedFollowedSeriesRow(t, db, idC)
			seedDiscoveryListsRow(t, db, idC, 1)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 50)
			require.NoError(t, err)

			byID := make(map[domain.SeriesID]RefreshCandidate, len(rows))
			occurrences := make(map[domain.SeriesID]int, len(rows))
			for _, r := range rows {
				byID[r.SeriesID] = r
				occurrences[r.SeriesID]++
			}

			require.Contains(t, byID, idA, "followed-not-in-library series must be picked")
			assert.Equal(t, enrichment.RefreshTierFollowed, byID[idA].Tier,
				"F-04: followed-not-in-library lands FOLLOWED, not Cold")

			require.Contains(t, byID, idB, "followed+in-library series must be picked")
			assert.Equal(t, enrichment.RefreshTierHot, byID[idB].Tier,
				"Hot wins over Followed for a followed+in-library series")

			require.Contains(t, byID, idC, "followed+in-discovery series must be picked")
			assert.Equal(t, enrichment.RefreshTierFollowed, byID[idC].Tier,
				"Followed wins over Normal for a followed+in-discovery series")

			for id, n := range occurrences {
				assert.Equalf(t, 1, n, "series %d appears %d times in the union, want exactly once", int64(id), n)
			}
		})
	}
}

// TestSeriesRepository_PickRefreshCandidates_FollowedTTLBoundary asserts the
// followed 10d TTL cutoff: a followed series synced 9d ago is fresh (excluded);
// synced 11d ago is stale and picked as FOLLOWED.
func TestSeriesRepository_PickRefreshCandidates_FollowedTTLBoundary(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			d9 := now.Add(-9 * 24 * time.Hour)   // < followed TTL (10d) → fresh
			d11 := now.Add(-11 * 24 * time.Hour) // > followed TTL (10d) → stale
			seedEnrichUser(t, db)

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

			idFresh := seedAndUpsert("fresh-9d", 4101, &d9)
			seedFollowedSeriesRow(t, db, idFresh)

			idStale := seedAndUpsert("stale-11d", 4102, &d11)
			seedFollowedSeriesRow(t, db, idStale)

			rows, err := repo.PickRefreshCandidates(ctx, now, enrichment.DefaultRefreshTTL(), 50)
			require.NoError(t, err)

			byID := make(map[domain.SeriesID]RefreshCandidate, len(rows))
			for _, r := range rows {
				byID[r.SeriesID] = r
			}

			assert.NotContains(t, byID, idFresh, "followed series synced 9d ago is within the 10d TTL → excluded")
			require.Contains(t, byID, idStale, "followed series synced 11d ago is past the 10d TTL → picked")
			assert.Equal(t, enrichment.RefreshTierFollowed, byID[idStale].Tier)
		})
	}
}
