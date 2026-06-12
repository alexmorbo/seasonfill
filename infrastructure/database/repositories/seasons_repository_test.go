package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/series"
)

func TestSeasonsRepository_UpsertAndList(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repoS := NewSeriesRepository(db)
	repo := NewSeasonsRepository(db)
	ctx := context.Background()

	seriesID, err := repoS.Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)

	id1, err := repo.Upsert(ctx, series.CanonSeason{SeriesID: seriesID, SeasonNumber: 1, Name: ptrString("Season 1")})
	require.NoError(t, err)
	id2, err := repo.Upsert(ctx, series.CanonSeason{SeriesID: seriesID, SeasonNumber: 2, Name: ptrString("Season 2")})
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2)

	rows, err := repo.ListBySeries(ctx, seriesID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, 1, rows[0].SeasonNumber)
	assert.Equal(t, 2, rows[1].SeasonNumber)
}

func TestSeasonsRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	seriesID, err := NewSeriesRepository(db).Upsert(context.Background(), sampleCanon("Severance"))
	require.NoError(t, err)
	repo := NewSeasonsRepository(db)
	ctx := context.Background()

	s := series.CanonSeason{SeriesID: seriesID, SeasonNumber: 1, EpisodeCount: ptrInt(9)}
	id1, err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	id2, err := repo.Upsert(ctx, s)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "natural-key conflict must reuse the row")
}
