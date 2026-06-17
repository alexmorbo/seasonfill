package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/grab"
)

// TestGrabRepository_CountImportedEpisodes verifies the SQL counter
// used by the evaluator to snapshot GrabbedEpisodes onto every
// Decision. Three rows are seeded; only the two "imported" rows in the
// target triple should count.
func TestGrabRepository_CountImportedEpisodes(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	now := time.Now().UTC()

	mk := func(status grab.Status, season int, instance string) grab.Record {
		return grab.Record{
			ID:           uuid.New(),
			InstanceName: instance,
			SeriesID:     42,
			SeriesTitle:  "Test",
			SeasonNumber: season,
			ReleaseGUID:  uuid.New().String(),
			ReleaseTitle: "test release",
			IndexerID:    1,
			IndexerName:  "RT",
			Quality:      "WEBDL-2160p",
			Status:       status,
			ScanRunID:    uuid.New(),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	}

	require.NoError(t, repo.Create(ctx, mk(grab.StatusImported, 1, "homelab")))
	require.NoError(t, repo.Create(ctx, mk(grab.StatusImported, 1, "homelab")))
	require.NoError(t, repo.Create(ctx, mk(grab.StatusGrabbed, 1, "homelab")))  // not imported
	require.NoError(t, repo.Create(ctx, mk(grab.StatusImported, 2, "homelab"))) // different season
	require.NoError(t, repo.Create(ctx, mk(grab.StatusImported, 1, "alpha")))   // different instance

	count, err := repo.CountImportedEpisodes(ctx, "homelab", 42, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Empty triple: zero, no error.
	count, err = repo.CountImportedEpisodes(ctx, "homelab", 99, 99)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}
