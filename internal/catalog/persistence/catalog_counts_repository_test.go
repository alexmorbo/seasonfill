package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestCatalogCountsRepository_EmptyDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)

			got, err := NewCatalogCountsRepository(db).Counts(ctx)
			require.NoError(t, err)
			assert.Equal(t, CatalogCounts{}, got, "empty DB must count zeros")
		})
	}
}

func TestCatalogCountsRepository_Counts(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)

			seriesRepo := NewSeriesRepository(db)
			seasonsRepo := enrichpersistence.NewSeasonsRepository(db)
			episodesRepo := NewEpisodesRepository(db)

			// Two series with distinct TMDB ids (unique index).
			c1 := sampleCanon("First")
			c1.TMDBID = ptrTMDBID(2001)
			sid1, err := seriesRepo.Upsert(ctx, c1)
			require.NoError(t, err)

			c2 := sampleCanon("Second")
			c2.TMDBID = ptrTMDBID(2002)
			sid2, err := seriesRepo.Upsert(ctx, c2)
			require.NoError(t, err)

			// Three seasons: two under series 1, one under series 2.
			_, err = seasonsRepo.Upsert(ctx, series.CanonSeason{SeriesID: sid1, SeasonNumber: 1})
			require.NoError(t, err)
			_, err = seasonsRepo.Upsert(ctx, series.CanonSeason{SeriesID: sid1, SeasonNumber: 2})
			require.NoError(t, err)
			_, err = seasonsRepo.Upsert(ctx, series.CanonSeason{SeriesID: sid2, SeasonNumber: 1})
			require.NoError(t, err)

			// Five episodes spread across the seasons.
			eps := []series.CanonEpisode{
				{SeriesID: sid1, SeasonNumber: 1, EpisodeNumber: 1},
				{SeriesID: sid1, SeasonNumber: 1, EpisodeNumber: 2},
				{SeriesID: sid1, SeasonNumber: 2, EpisodeNumber: 1},
				{SeriesID: sid2, SeasonNumber: 1, EpisodeNumber: 1},
				{SeriesID: sid2, SeasonNumber: 1, EpisodeNumber: 2},
			}
			_, err = episodesRepo.BatchUpsert(ctx, eps)
			require.NoError(t, err)

			got, err := NewCatalogCountsRepository(db).Counts(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(2), got.Series)
			assert.Equal(t, int64(3), got.Seasons)
			assert.Equal(t, int64(5), got.Episodes)
		})
	}
}
