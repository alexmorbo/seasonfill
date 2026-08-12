package wiring

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	reqapp "github.com/alexmorbo/seasonfill/internal/request/app"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	reqpersistence "github.com/alexmorbo/seasonfill/internal/request/persistence"
	reqrest "github.com/alexmorbo/seasonfill/internal/request/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// sonarrReplayAdapter maps a stored AddSpec back to a Sonarr add. Username=""
// → the gate is bypassed (direct add), so approve never re-queues.
type sonarrReplayAdapter struct{ uc *discoapp.AddToSonarrUseCase }

func (a sonarrReplayAdapter) AddTV(ctx context.Context, spec reqdomain.AddSpec) error {
	_, err := a.uc.Add(ctx, discoapp.AddRequest{
		InstanceName:     domain.InstanceName(spec.InstanceName),
		TVDBID:           int(spec.ExternalID),
		QualityProfileID: spec.QualityProfileID,
		RootFolderPath:   spec.RootFolderPath,
		Monitored:        spec.Monitored,
		MonitorMode:      spec.MonitorMode,
		SearchOnAdd:      spec.SearchOnAdd,
		Username:         "", // system replay — bypass the request gate
		MonitoredSeasons: spec.Seasons,
	})
	return err
}

// radarrReplayAdapter maps a stored AddSpec back to a Radarr add.
type radarrReplayAdapter struct{ uc *discoapp.AddToRadarrUseCase }

func (a radarrReplayAdapter) AddMovie(ctx context.Context, spec reqdomain.AddSpec) error {
	_, err := a.uc.Add(ctx, discoapp.AddMovieRequest{
		InstanceName:        domain.InstanceName(spec.InstanceName),
		TMDBID:              int(spec.ExternalID),
		QualityProfileID:    spec.QualityProfileID,
		RootFolderPath:      spec.RootFolderPath,
		Monitored:           spec.Monitored,
		MinimumAvailability: spec.MinimumAvailability,
		SearchOnAdd:         spec.SearchOnAdd,
		Username:            "", // system replay — bypass the request gate
	})
	return err
}

// RequestsBundle carries the wired request-workflow pieces.
type RequestsBundle struct {
	Handler *reqrest.RequestHandler
	Queue   discoapp.RequestQueue // injected into the add use cases
	UseCase *reqapp.UseCase
}

// BuildRequests wires the request-workflow use case + handler. sonarrAdd/
// radarrAdd are the SAME discovery use cases the direct-add routes use (so
// approve replays through them). outbox + tx come from the notification/scan
// bundles. userRepo resolves the caller in the handler.
func BuildRequests(
	db *gorm.DB,
	sonarrAdd *discoapp.AddToSonarrUseCase,
	radarrAdd *discoapp.AddToRadarrUseCase,
	userRepo ports.UserRepository,
	outbox ports.OutboxEmitter,
	tx reqapp.Transactor,
	log *slog.Logger,
) RequestsBundle {
	repo := reqpersistence.NewRequestRepository(db)
	uc := reqapp.NewUseCase(
		repo,
		sonarrReplayAdapter{uc: sonarrAdd},
		radarrReplayAdapter{uc: radarrAdd},
		outbox,
		tx,
		log,
	)
	return RequestsBundle{
		Handler: reqrest.NewRequestHandler(uc, userRepo, log),
		Queue:   uc,
		UseCase: uc,
	}
}
