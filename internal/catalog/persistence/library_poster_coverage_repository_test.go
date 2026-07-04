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

// TestLibraryPosterCoverageRepository_MultiInstanceCountsDistinct proves the
// gauges count DISTINCT canonical series, not series_cache rows: the SAME
// series present in two Sonarr instances (two series_cache rows resolving to
// one canon via TMDB natural-key dedup) is tallied ONCE in both total and
// covered, so multi-instance duplication cannot inflate the absolute gauges.
func TestLibraryPosterCoverageRepository_MultiInstanceCountsDistinct(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)

			cacheRepo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			mediaRepo := enrichpersistence.NewSeriesMediaTextsRepository(db)

			// Same series in two instances: identical TMDB/TVDB/IMDB so both
			// series_cache rows dedup onto ONE canonical series_id. Distinct
			// (instance, sonarr_id) keeps the two cache rows from colliding.
			shTMDB := domain.TMDBID(777001)
			shTVDB := domain.TVDBID(888001)
			shIMDB := domain.IMDBID("tt7770010")
			shared := func(inst domain.InstanceName, sonarrID domain.SonarrSeriesID) series.CacheEntry {
				e := sampleEntry(inst, sonarrID)
				e.TMDBID = &shTMDB
				e.TVDBID = &shTVDB
				e.IMDBID = &shIMDB
				return e
			}
			require.NoError(t, cacheRepo.Upsert(ctx, shared(domain.InstanceName("homelab"), 1)))
			require.NoError(t, cacheRepo.Upsert(ctx, shared(domain.InstanceName("backup"), 2)))

			// Sanity: two cache rows, one canon.
			sid := canonIDForCache(t, db, domain.InstanceName("homelab"), 1)
			sidB := canonIDForCache(t, db, domain.InstanceName("backup"), 2)
			require.Equal(t, sid, sidB, "both instances must resolve to one canon series_id")

			poster := "/canon/shared-poster.jpg"
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid, Language: "en-US", PosterAsset: &poster,
			}))

			got, err := NewLibraryPosterCoverageRepository(db).LibraryPosterCoverage(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(1), got.Total, "two instances of one series count as 1 (distinct series_id)")
			assert.Equal(t, int64(1), got.Covered, "the covered series is counted once, not per instance")
		})
	}
}
