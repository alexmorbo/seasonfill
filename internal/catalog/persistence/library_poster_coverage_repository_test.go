package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestLibraryPosterCoverageRepository_EmptyDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)

			got, err := NewLibraryPosterCoverageRepository(db).LibraryPosterCoverage(ctx)
			require.NoError(t, err)
			assert.Equal(t, LibraryPosterCoverage{}, got, "empty DB must count zeros")
		})
	}
}

func TestLibraryPosterCoverageRepository_Coverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)

			const inst = domain.InstanceName("homelab")
			cacheRepo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			mediaRepo := enrichpersistence.NewSeriesMediaTextsRepository(db)

			// Series 1: library row WITH a poster_asset media text (covered).
			require.NoError(t, cacheRepo.Upsert(ctx, sampleEntry(inst, 1)))
			sid1 := canonIDForCache(t, db, inst, 1)
			poster := "/canon/poster1.jpg"
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid1, Language: "en-US", PosterAsset: &poster,
			}))

			// Series 2: library row with NO media text (uncovered).
			require.NoError(t, cacheRepo.Upsert(ctx, sampleEntry(inst, 2)))

			// Series 3: media text present but poster_asset NULL → NOT covered.
			require.NoError(t, cacheRepo.Upsert(ctx, sampleEntry(inst, 3)))
			sid3 := canonIDForCache(t, db, inst, 3)
			backdrop := "/canon/backdrop3.jpg"
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid3, Language: "en-US", BackdropAsset: &backdrop,
			}))

			got, err := NewLibraryPosterCoverageRepository(db).LibraryPosterCoverage(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(3), got.Total, "all 3 non-deleted library series count")
			assert.Equal(t, int64(1), got.Covered, "only series 1 has a poster_asset")
		})
	}
}
