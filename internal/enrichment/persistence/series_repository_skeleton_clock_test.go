package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// W18-16: MarkSkeletonSynced must advance the dedicated skeleton_synced_at clock
// WITHOUT re-timing the shared enrichment_tmdb_synced_at (the full-enrichment TTL
// gate that HandleForcedLang deliberately never stamps). Real DB so the actual
// UPDATE SQL + migration 000034 column are exercised.
func TestSeriesRepository_MarkSkeletonSynced_IsolatedFromEnrichmentClock(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gdb := backend.NewDB(t)
			repo := NewSeriesRepository(gdb)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCanon("W18-16 Skeleton Clock"))
			require.NoError(t, err)
			require.NotZero(t, id)

			// Seed a KNOWN-STALE full-enrichment clock (the sentinel that must NOT move).
			stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			require.NoError(t, gdb.WithContext(ctx).Table("series").
				Where("id = ?", int64(id)).
				Update("enrichment_tmdb_synced_at", stale).Error)

			// Precondition: skeleton clock starts NULL (sampleCanon leaves it nil).
			pre, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.Nil(t, pre.SkeletonSyncedAt, "skeleton clock must start NULL")

			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, repo.MarkSkeletonSynced(ctx, id, now))

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.SkeletonSyncedAt, "skeleton clock must be stamped")
			assert.WithinDuration(t, now, got.SkeletonSyncedAt.UTC(), 2*time.Second,
				"skeleton_synced_at must advance to now")
			require.NotNil(t, got.EnrichmentTMDBSyncedAt, "full-enrichment clock must survive")
			assert.WithinDuration(t, stale, got.EnrichmentTMDBSyncedAt.UTC(), time.Second,
				"MarkSkeletonSynced must NOT re-time enrichment_tmdb_synced_at (W18-16)")
		})
	}
}
