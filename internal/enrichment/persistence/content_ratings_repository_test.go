package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestContentRatingsRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			repo := NewContentRatingsRepository(db)

			require.NoError(t, repo.Upsert(ctx, database.ContentRatingModel{
				SeriesID: seriesID, CountryCode: "US", Rating: "TV-MA",
			}))

			got, err := repo.Get(ctx, seriesID, "US")
			require.NoError(t, err)
			assert.Equal(t, "TV-MA", got.Rating)
		})
	}
}

func TestContentRatingsRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewContentRatingsRepository(db)
			_, err := repo.Get(context.Background(), 1, "US")
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestContentRatingsRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			repo := NewContentRatingsRepository(db)

			row := database.ContentRatingModel{
				SeriesID: seriesID, CountryCode: "RU", Rating: "16+",
			}
			require.NoError(t, repo.Upsert(ctx, row))
			require.NoError(t, repo.Upsert(ctx, row))

			got, err := repo.Get(ctx, seriesID, "RU")
			require.NoError(t, err)
			assert.Equal(t, "16+", got.Rating)
		})
	}
}

func TestContentRatingsRepository_ListBySeries(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
			require.NoError(t, err)
			repo := NewContentRatingsRepository(db)

			require.NoError(t, repo.Upsert(ctx, database.ContentRatingModel{
				SeriesID: seriesID, CountryCode: "US", Rating: "TV-14",
			}))
			require.NoError(t, repo.Upsert(ctx, database.ContentRatingModel{
				SeriesID: seriesID, CountryCode: "GB", Rating: "15",
			}))
			require.NoError(t, repo.Upsert(ctx, database.ContentRatingModel{
				SeriesID: seriesID, CountryCode: "RU", Rating: "16+",
			}))

			rows, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Len(t, rows, 3)
			// Ordered by country_code ASC.
			assert.Equal(t, "GB", rows[0].CountryCode)
			assert.Equal(t, "RU", rows[1].CountryCode)
			assert.Equal(t, "US", rows[2].CountryCode)
		})
	}
}
