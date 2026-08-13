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
// Ф6-R-6b library list).
type MovieDetailBundle struct {
	Handler        *mdrest.Handler
	LibraryHandler *catalogrest.MovieLibraryHandler
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
	).WithHydrationTrigger(movieRepo, time.Now, domainLog)
	// Ф6-R-6b — global movie library list (GET /api/v1/movies).
	movieI18nRead := enrichpersistence.NewMovieI18nReadRepository(db)
	libraryHandler := catalogrest.NewMovieLibraryHandler(
		catalogpersistence.NewMovieLibraryRepository(db), resolver, domainLog).
		WithLocalizer(movieI18nRead)
	return &MovieDetailBundle{
		Handler:        mdrest.NewHandler(uc, resolver, domainLog),
		LibraryHandler: libraryHandler,
	}
}
