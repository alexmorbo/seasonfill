package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	mdrest "github.com/alexmorbo/seasonfill/internal/moviedetail/rest"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieDetailBundle groups the movie-detail read handler (Ф6-R-6a).
type MovieDetailBundle struct {
	Handler *mdrest.Handler
}

// BuildMovieDetail wires the read-only movie-detail aggregate over local repos.
func BuildMovieDetail(db *gorm.DB, log *slog.Logger) *MovieDetailBundle {
	domainLog := sharedports.DomainLogger(log, "http")
	uc := mdapp.New(
		enrichpersistence.NewMovieRepository(db),
		enrichpersistence.NewMovieI18nReadRepository(db),
		enrichpersistence.NewMovieCollectionsRepository(db),
		catalogpersistence.NewMovieStatesRepository(db),
	)
	return &MovieDetailBundle{Handler: mdrest.NewHandler(uc, domainLog)}
}
