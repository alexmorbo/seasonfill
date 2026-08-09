package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesRepository_ListOrphanCandidates_ExcludesFollowed covers the
// ADR-0015 Ф3 C1 followed_series exclusion in ListOrphanCandidates (F-04
// data-loss guard). An old canon with no series_cache and no
// series_recommendations IS an orphan candidate — until a followed_series row
// references it, at which point it must NOT be returned. Unfollowing restores
// candidacy.
func TestSeriesRepository_ListOrphanCandidates_ExcludesFollowed(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
			cutoff := now.Add(-90 * 24 * time.Hour)
			old := now.Add(-120 * 24 * time.Hour) // older than the 90d cutoff

			id, err := repo.Upsert(ctx, sampleCanon("orphan-followed"))
			require.NoError(t, err)
			// Force created_at older than the cutoff (Upsert stamps now()).
			require.NoError(t, db.Model(&database.SeriesModel{}).
				Where("id = ?", id).
				UpdateColumn("created_at", old).Error)

			// Baseline: unreferenced old canon IS an orphan candidate.
			ids, err := repo.ListOrphanCandidates(ctx, cutoff, 100)
			require.NoError(t, err)
			assert.Contains(t, ids, id, "unreferenced old canon must be an orphan candidate")

			// Follow it → excluded from candidates.
			require.NoError(t, db.Create(&database.FollowedSeriesModel{
				SeriesID: int64(id), CreatedAt: now,
			}).Error)
			ids, err = repo.ListOrphanCandidates(ctx, cutoff, 100)
			require.NoError(t, err)
			assert.NotContains(t, ids, id, "followed series must NOT be an orphan candidate (F-04)")

			// Unfollow → candidate again.
			require.NoError(t, db.Where("series_id = ?", int64(id)).
				Delete(&database.FollowedSeriesModel{}).Error)
			ids, err = repo.ListOrphanCandidates(ctx, cutoff, 100)
			require.NoError(t, err)
			assert.Contains(t, ids, id, "unfollowed series is an orphan candidate again")
		})
	}
}
