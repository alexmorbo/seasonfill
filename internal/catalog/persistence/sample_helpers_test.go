package persistence

// Test helpers carried over from infrastructure/database/repositories/
// sample_helpers_test.go when the catalog repository slice graduated
// to internal/catalog/persistence (story 443 A-1-17). The catalog test
// suite seeds canon series + people via the moved enrichment-persistence
// constructors. Keeping the shorter helper signatures + sample fixtures
// available here means each test still calls NewSeriesRepository(db) /
// sampleCanon("...") with the same shape it used pre-move.
//
// Future story will collapse the enrichpersistence aliases into a
// shared testhelpers package when the model split lands.

import (
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// sampleCanon mirrors the persistence-package fixture used by every
// catalog repo test. Values are identical (TMDB=101 / TVDB=202 /
// IMDB=tt0000001) to the legacy infrastructure/database/repositories
// sampleCanon so a test that upserts a canon and then asks the moved
// SeriesRepository for it gets the same row.
func sampleCanon(title string) series.Canon {
	return series.Canon{
		Title:         title,
		Hydration:     series.HydrationStub,
		TMDBID:        ptrTMDBID(101),
		TVDBID:        ptrTVDBID(202),
		IMDBID:        ptrIMDBID("tt0000001"),
		OriginalTitle: new("orig: " + title),
		Status:        new("Returning Series"),
		Year:          new(2024),
		InProduction:  true,
	}
}

// Aliases so catalog tests keep their `NewSeriesRepository(db)` call
// sites unchanged. These resolve to the moved constructors in
// internal/enrichment/persistence.
var (
	NewSeriesRepository   = enrichpersistence.NewSeriesRepository
	NewEpisodesRepository = enrichpersistence.NewEpisodesRepository
	NewNetworksRepository = enrichpersistence.NewNetworksRepository
)

// Type aliases so a catalog test can keep `*SeriesRepository` shape
// annotations and `_ var = (*SeriesRepository)(nil)` assertions
// running without import churn.
type (
	SeriesRepository   = enrichpersistence.SeriesRepository
	EpisodesRepository = enrichpersistence.EpisodesRepository
	NetworksRepository = enrichpersistence.NetworksRepository
)

// ptrTMDBID / ptrIMDBID / ptrTVDBID are thin pointer-to-typed-ID
// shorthands used by the catalog series_cache / episode_states /
// season_stats tests. They mirror the helpers in
// internal/enrichment/persistence/grab_test_helpers_test.go verbatim
// so the catalog tests don't need to reach into that package.
func ptrTMDBID(i int) *domain.TMDBID {
	v := domain.TMDBID(i)
	return &v
}

func ptrIMDBID(s string) *domain.IMDBID {
	v := domain.IMDBID(s)
	return &v
}

func ptrTVDBID(i int) *domain.TVDBID {
	v := domain.TVDBID(i)
	return &v
}

// _ keeps the `domain` import alive when this file is the only
// consumer in the catalog test set; `series` is already referenced by
// sampleCanon's return type.
var _ = domain.SeriesID(0)
