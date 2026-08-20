package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	followapp "github.com/alexmorbo/seasonfill/internal/follow/app"
	followpersistence "github.com/alexmorbo/seasonfill/internal/follow/persistence"
	followrest "github.com/alexmorbo/seasonfill/internal/follow/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// FollowBundle carries the follow feature's wired handler.
type FollowBundle struct {
	Handler *followrest.FollowHandler
}

// NewFollowBundle builds the follow use case + handler. seriesReader is the
// enrichment SeriesRepository (Get); enricher is the SAME OnDemandEnricherHolder
// ResolveUseCase uses (nil-OK).
func NewFollowBundle(
	db *gorm.DB,
	seriesReader followapp.SeriesReader,
	enricher followapp.Enricher,
	users ports.UserRepository,
	log *slog.Logger,
) (FollowBundle, error) {
	repo := followpersistence.NewFollowedSeriesRepository(db)
	uc, err := followapp.NewFollowUseCase(seriesReader, repo, enricher, log)
	if err != nil {
		return FollowBundle{}, err
	}
	return FollowBundle{Handler: followrest.NewFollowHandler(uc, users, log)}, nil
}

// MovieFollowBundle carries the movie-follow feature's wired handler.
type MovieFollowBundle struct {
	Handler *followrest.MovieFollowHandler
}

// NewMovieFollowBundle builds the movie follow use case + handler (ADR-0022
// Wave-3, the movie mirror of NewFollowBundle). movieReader is the enrichment
// MovieRepository (GetByTMDBID); enricher is the SAME MovieHotEnqueuer holder
// the movie-detail read enrolls through (nil-OK).
func NewMovieFollowBundle(
	db *gorm.DB,
	movieReader followapp.MovieReader,
	enricher followapp.MovieEnricher,
	users ports.UserRepository,
	log *slog.Logger,
) (MovieFollowBundle, error) {
	repo := followpersistence.NewFollowedMoviesRepository(db)
	uc, err := followapp.NewMovieFollowUseCase(movieReader, repo, enricher, log)
	if err != nil {
		return MovieFollowBundle{}, err
	}
	return MovieFollowBundle{Handler: followrest.NewMovieFollowHandler(uc, users, log)}, nil
}
