package repositories

// Test helpers shared by stays-in-place repos (episode_states,
// series_cache, videos, watchdog_seasons, sonarr_instance,
// content_ratings, external_ids, i18n_texts, etc.) that still need to
// seed series canon + people via the moved enrichment-persistence
// constructors. Story 437 (A-1-11) moved series/seasons/episodes +
// people repos to internal/enrichment/persistence; this file keeps the
// shorter helper signatures + sample fixtures available to the stays
// without forcing every stay test to learn the enrichpersistence
// alias.
//
// Future story will migrate the remaining catalog stays (catalog
// taxonomy + i18n + recommendations + media_assets etc.) into
// enrichment/persistence as well, at which point this file collapses
// into the persistence package's own sample_helpers_test.go.

import (
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// sampleCanon mirrors the persistence-package fixture used by every
// catalog repo test. Kept here verbatim so the stays don't need to
// reach into persistence's unexported sampleCanon — the values are
// identical (TMDB=101 / TVDB=202 / IMDB=tt0000001) so a stay test that
// upserts a canon and then asks the moved SeriesRepository for it gets
// the same row.
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

// Aliases so stay tests can keep their `NewSeriesRepository(db)` call
// sites unchanged. These resolve to the moved constructors in
// internal/enrichment/persistence.
var (
	NewSeriesRepository          = enrichpersistence.NewSeriesRepository
	NewSeasonsRepository         = enrichpersistence.NewSeasonsRepository
	NewEpisodesRepository        = enrichpersistence.NewEpisodesRepository
	NewPeopleRepository          = enrichpersistence.NewPeopleRepository
	NewPersonCreditsRepository   = enrichpersistence.NewPersonCreditsRepository
	NewSeriesPeopleRepository    = enrichpersistence.NewSeriesPeopleRepository
	NewEpisodePeopleRepository   = enrichpersistence.NewEpisodePeopleRepository
	NewGenresRepository          = enrichpersistence.NewGenresRepository
	NewGenresI18nRepository      = enrichpersistence.NewGenresI18nRepository
	NewNetworksRepository        = enrichpersistence.NewNetworksRepository
	NewCompaniesRepository       = enrichpersistence.NewCompaniesRepository
	NewKeywordsRepository        = enrichpersistence.NewKeywordsRepository
	NewKeywordsI18nRepository    = enrichpersistence.NewKeywordsI18nRepository
	NewSeriesTextsRepository     = enrichpersistence.NewSeriesTextsRepository
	NewEpisodeTextsRepository    = enrichpersistence.NewEpisodeTextsRepository
	NewExternalIDsRepository     = enrichpersistence.NewExternalIDsRepository
	NewContentRatingsRepository  = enrichpersistence.NewContentRatingsRepository
	NewOriginReleaseRepository   = enrichpersistence.NewOriginReleaseRepository
	NewRecommendationsRepository = enrichpersistence.NewRecommendationsRepository
	NewLiveAssetsRepository      = enrichpersistence.NewLiveAssetsRepository
	NewMediaAssetsRepository     = enrichpersistence.NewMediaAssetsRepository
)

// Type aliases so a stay test can keep `*SeriesRepository` shape
// annotations and `_ var = (*SeriesRepository)(nil)` assertions
// running without import churn.
type (
	SeriesRepository          = enrichpersistence.SeriesRepository
	SeasonsRepository         = enrichpersistence.SeasonsRepository
	EpisodesRepository        = enrichpersistence.EpisodesRepository
	PeopleRepository          = enrichpersistence.PeopleRepository
	PersonCreditsRepository   = enrichpersistence.PersonCreditsRepository
	SeriesPeopleRepository    = enrichpersistence.SeriesPeopleRepository
	EpisodePeopleRepository   = enrichpersistence.EpisodePeopleRepository
	GenresRepository          = enrichpersistence.GenresRepository
	GenresI18nRepository      = enrichpersistence.GenresI18nRepository
	NetworksRepository        = enrichpersistence.NetworksRepository
	CompaniesRepository       = enrichpersistence.CompaniesRepository
	KeywordsRepository        = enrichpersistence.KeywordsRepository
	KeywordsI18nRepository    = enrichpersistence.KeywordsI18nRepository
	SeriesTextsRepository     = enrichpersistence.SeriesTextsRepository
	EpisodeTextsRepository    = enrichpersistence.EpisodeTextsRepository
	ExternalIDsRepository     = enrichpersistence.ExternalIDsRepository
	ContentRatingsRepository  = enrichpersistence.ContentRatingsRepository
	OriginReleaseRepository   = enrichpersistence.OriginReleaseRepository
	RecommendationsRepository = enrichpersistence.RecommendationsRepository
	LiveAssetsRepository      = enrichpersistence.LiveAssetsRepository
	MediaAssetsRepository     = enrichpersistence.MediaAssetsRepository
)

// _ keeps the `domain` import alive when this file is the only
// consumer in the stays test set; `series` is already referenced by
// sampleCanon's return type.
var _ = domain.SeriesID(0)
