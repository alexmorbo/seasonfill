package wiring

import (
	"log/slog"
	"time"

	"gorm.io/gorm"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	mdrest "github.com/alexmorbo/seasonfill/internal/moviedetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieDetailBundle groups the movie read handlers (Ф6-R-6a detail +
// Ф6-R-6b library list + Ф2.1 cast sub-endpoint).
type MovieDetailBundle struct {
	Handler                *mdrest.Handler
	LibraryHandler         *catalogrest.MovieLibraryHandler
	CastHandler            *mdrest.MovieCastHandler            // Ф2.1
	CastETagFreshness      *mdapp.MovieETagFreshnessAdapter    // Ф2.1 first live movie ETag wiring
	OverviewHandler        *mdrest.MovieOverviewHandler        // Ф2.2 movie overview sub-endpoint
	RatingsHandler         *mdrest.MovieRatingsHandler         // Ф2.3 movie ratings sub-endpoint
	RecommendationsHandler *mdrest.MovieRecommendationsHandler // Ф2.4 movie recommendations sub-endpoint
}

// BuildMovieDetail wires the read-only movie-detail aggregate + the movie
// library list, both over local repos (movies canon + movie_states).
func BuildMovieDetail(db *gorm.DB, resolver *media.Resolver, log *slog.Logger) *MovieDetailBundle {
	domainLog := sharedports.DomainLogger(log, "http")
	movieRepo := enrichpersistence.NewMovieRepository(db)
	uc := mdapp.New(
		movieRepo,
		enrichpersistence.NewMovieI18nReadRepository(db),
		enrichpersistence.NewMovieCollectionsRepository(db),
		catalogpersistence.NewMovieStatesRepository(db),
	).
		WithHydrationTrigger(movieRepo, time.Now, domainLog).
		WithTaxonomy(
			enrichpersistence.NewGenresRepository(db),
			enrichpersistence.NewKeywordsRepository(db),
		)
	// Ф6-R-6b — global movie library list (GET /api/v1/movies).
	movieI18nRead := enrichpersistence.NewMovieI18nReadRepository(db)
	libraryHandler := catalogrest.NewMovieLibraryHandler(
		catalogpersistence.NewMovieLibraryRepository(db), resolver, domainLog).
		WithLocalizer(movieI18nRead)
	// Ф2.1 — movie cast sub-endpoint. Reuses the person_credits reverse reader +
	// people name reader; movieI18nRead drives served_language via TitleLanguage.
	castUC := mdapp.NewCastUseCase(
		movieRepo,
		enrichpersistence.NewPersonCreditsRepository(db),
		enrichpersistence.NewPeopleRepository(db),
		movieI18nRead,
	)
	castHandler := mdrest.NewMovieCastHandler(castUC, resolver, domainLog)
	castETag := mdapp.NewMovieETagFreshnessAdapter(movieRepo)
	// Ф2.2 — movie overview sub-endpoint. movieI18nRead drives both the per-field
	// text ladder (Get) and served_language (TitleLanguage); movieRepo = canon.
	overviewUC := mdapp.NewOverviewUseCase(movieRepo, movieI18nRead, movieI18nRead)
	overviewHandler := mdrest.NewMovieOverviewHandler(overviewUC, domainLog)
	// Ф2.3 — movie ratings sub-endpoint. Read-only over the canon row (movieRepo);
	// no localization, no ETag, no live refresh.
	ratingsUC := mdapp.NewRatingsUseCase(movieRepo)
	ratingsHandler := mdrest.NewMovieRatingsHandler(ratingsUC, domainLog)
	// Ф2.4 — movie recommendations sub-endpoint. Read-only over local repos:
	// movie_recommendations rank list + movies canon batch (movieRepo doubles as
	// CanonReader and MovieCanonBatchReader). Reuses the movie ETag adapter (recs
	// section → enrichment_recs_synced_at) already built as castETag.
	recsUC := mdapp.NewRecommendationsUseCase(
		movieRepo,
		enrichpersistence.NewMovieRecommendationsRepository(db),
		movieRepo,
	)
	recsHandler := mdrest.NewMovieRecommendationsHandler(recsUC, resolver, domainLog)
	return &MovieDetailBundle{
		Handler:                mdrest.NewHandler(uc, resolver, domainLog),
		LibraryHandler:         libraryHandler,
		CastHandler:            castHandler,
		CastETagFreshness:      castETag,
		OverviewHandler:        overviewHandler,
		RatingsHandler:         ratingsHandler,
		RecommendationsHandler: recsHandler,
	}
}
