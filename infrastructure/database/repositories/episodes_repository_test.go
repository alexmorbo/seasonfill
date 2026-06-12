package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/series"
)

func TestEpisodesRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	repo := NewEpisodesRepository(db)

	id, err := repo.Upsert(ctx, series.CanonEpisode{
		SeriesID:      seriesID,
		SeasonNumber:  1,
		EpisodeNumber: 1,
		AirDate:       ptrTime(time.Date(2022, 9, 21, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, seriesID, got.SeriesID)
	assert.Equal(t, 1, got.SeasonNumber)
	assert.Equal(t, 1, got.EpisodeNumber)
}

func TestEpisodesRepository_BatchUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	repo := NewEpisodesRepository(db)

	const n = 500
	episodes := make([]series.CanonEpisode, n)
	for i := 0; i < n; i++ {
		episodes[i] = series.CanonEpisode{
			SeriesID:      seriesID,
			SeasonNumber:  1,
			EpisodeNumber: i + 1,
		}
	}
	start := time.Now()
	ids, err := repo.BatchUpsert(ctx, episodes)
	require.NoError(t, err)
	require.Len(t, ids, n)
	// Budget covers single-round-trip semantics under `-race`. Reference
	// non-race timing on dev macOS is ~150ms for 500 rows; -race + parallel
	// test load inflates that ~5x, so a 5s budget catches the regression
	// shape ("N round-trips" would be 10s+) without flaking on machine load.
	assert.Less(t, time.Since(start), 5*time.Second, "batch upsert must complete in one round-trip for 500 rows")

	// Re-batch with the same payload — every id must round-trip equal.
	ids2, err := repo.BatchUpsert(ctx, episodes)
	require.NoError(t, err)
	require.Equal(t, ids, ids2, "second batch must resolve to the same ids by natural key")
}

func TestEpisodesRepository_ListBySeason(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	repo := NewEpisodesRepository(db)
	for s := 1; s <= 2; s++ {
		for e := 1; e <= 3; e++ {
			_, err := repo.Upsert(ctx, series.CanonEpisode{
				SeriesID:      seriesID,
				SeasonNumber:  s,
				EpisodeNumber: e,
			})
			require.NoError(t, err)
		}
	}
	rows, err := repo.ListBySeason(ctx, seriesID, 2)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, 1, rows[0].EpisodeNumber)
	assert.Equal(t, 3, rows[2].EpisodeNumber)
}

func ptrTime(t time.Time) *time.Time { return &t }
