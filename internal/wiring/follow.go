package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	followapp "github.com/alexmorbo/seasonfill/internal/follow/app"
	followpersistence "github.com/alexmorbo/seasonfill/internal/follow/persistence"
	followrest "github.com/alexmorbo/seasonfill/internal/follow/rest"
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
	log *slog.Logger,
) (FollowBundle, error) {
	repo := followpersistence.NewFollowedSeriesRepository(db)
	uc, err := followapp.NewFollowUseCase(seriesReader, repo, enricher, log)
	if err != nil {
		return FollowBundle{}, err
	}
	return FollowBundle{Handler: followrest.NewFollowHandler(uc, log)}, nil
}
