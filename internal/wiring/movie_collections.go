package wiring

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecalendar"
	moviecollection "github.com/alexmorbo/seasonfill/internal/catalog/app/moviecollection"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// movieAdderBridge adapts *discoapp.AddToRadarrUseCase into
// moviecollection.MovieAdder (the batch add-missing seam). R-5 kept these
// packages decoupled; R-6a wires the concrete bridge.
type movieAdderBridge struct{ uc *discoapp.AddToRadarrUseCase }

var _ moviecollection.MovieAdder = movieAdderBridge{}

func (b movieAdderBridge) Add(ctx context.Context, req moviecollection.AddMovieRequest) (moviecollection.AddMovieOutcome, error) {
	res, err := b.uc.Add(ctx, discoapp.AddMovieRequest{
		InstanceName:        req.InstanceName,
		TMDBID:              req.TMDBID,
		QualityProfileID:    req.QualityProfileID,
		RootFolderPath:      req.RootFolderPath,
		Monitored:           req.Monitored,
		MinimumAvailability: req.MinimumAvailability,
		SearchOnAdd:         req.SearchOnAdd,
	})
	if err != nil {
		return moviecollection.AddMovieOutcome{}, err
	}
	return moviecollection.AddMovieOutcome{RadarrMovieID: res.RadarrMovieID, AlreadyAdded: res.AlreadyAdded}, nil
}

// BuildMovieCollections wires the three collection routes over the R-5 usecases.
func BuildMovieCollections(db *gorm.DB, radarr *RadarrSyncBundle, log *slog.Logger) *catalogrest.MovieCollectionsHandler {
	domainLog := sharedports.DomainLogger(log, "http")
	repo := enrichpersistence.NewMovieCollectionsRepository(db)
	addUC := discoapp.NewAddToRadarrUseCase(radarrAddLookup{holder: radarr.RadarrHolder}, domainLog)
	addAll := moviecollection.NewAddMissingUseCase(repo, movieAdderBridge{uc: addUC}, domainLog)
	monitor := moviecollection.NewRadarrMonitorUseCase(radarrCollectionLookup{holder: radarr.RadarrHolder}, repo, domainLog)
	defaultInstance := func() string {
		m := radarr.RadarrHolder.Load()
		if len(m) != 1 {
			return ""
		}
		for name := range m {
			return name
		}
		return ""
	}
	return catalogrest.NewMovieCollectionsHandler(repo, repo, addAll, monitor, defaultInstance, domainLog)
}

// BuildMovieCalendar wires the read-only movie release calendar over the movies
// release-date columns (Ф6-R-6a). Separate from the TV calendar (episode-shaped).
func BuildMovieCalendar(db *gorm.DB, log *slog.Logger) *catalogrest.MovieCalendarHandler {
	domainLog := sharedports.DomainLogger(log, "http")
	uc := moviecalendar.NewUseCase(catalogpersistence.NewMovieCalendarRepository(db))
	return catalogrest.NewMovieCalendarHandler(uc, domainLog)
}
