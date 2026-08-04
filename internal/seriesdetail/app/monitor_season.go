package seriesdetail

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

type MonitorSeasonResult struct {
	SonarrSeriesID domain.SonarrSeriesID
	SeasonNumber   int
	Monitored      bool
	Searched       bool
}

type MonitorSeasonDeps struct {
	CacheLookup SeriesCacheLookupPort
	SonarrFor   func(instanceName domain.InstanceName) (SonarrSeasonMonitor, bool)
	Logger      *slog.Logger
}

type MonitorSeasonUseCase struct {
	d MonitorSeasonDeps
}

func NewMonitorSeasonUseCase(d MonitorSeasonDeps) *MonitorSeasonUseCase {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &MonitorSeasonUseCase{d: d}
}

func (uc *MonitorSeasonUseCase) Execute(
	ctx context.Context,
	instanceName domain.InstanceName,
	seriesID domain.SeriesID,
	seasonNumber int,
	search bool,
) (MonitorSeasonResult, error) {
	entries, err := uc.d.CacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		return MonitorSeasonResult{}, fmt.Errorf("monitor season: list cache: %w", err)
	}
	entry, found := selectInstanceEntry(entries, instanceName)
	if !found {
		return MonitorSeasonResult{}, &sharedErrors.InstanceNotFoundError{Name: instanceName}
	}
	sonarrID := entry.SonarrSeriesID

	client, ok := uc.d.SonarrFor(instanceName)
	if !ok || client == nil {
		return MonitorSeasonResult{}, &sharedErrors.InstanceNotFoundError{Name: instanceName}
	}

	srs, err := client.GetSeries(ctx, sonarrID)
	if err != nil {
		return MonitorSeasonResult{}, &sharedErrors.SonarrUnreachableError{Instance: instanceName, Cause: err}
	}
	var seasonExists, alreadyMonitored bool
	for _, s := range srs.Seasons {
		if s.Number == seasonNumber {
			seasonExists = true
			alreadyMonitored = s.Monitored
			break
		}
	}
	if !seasonExists {
		return MonitorSeasonResult{}, &sharedErrors.SeasonNotFoundError{
			InstanceName: instanceName, SonarrSeriesID: sonarrID, SeasonNumber: seasonNumber,
		}
	}
	if alreadyMonitored {
		return MonitorSeasonResult{SonarrSeriesID: sonarrID, SeasonNumber: seasonNumber, Monitored: true, Searched: false}, nil
	}

	if err := client.SetSeasonMonitored(ctx, sonarrID, seasonNumber, true); err != nil {
		return MonitorSeasonResult{}, &sharedErrors.SonarrUnreachableError{Instance: instanceName, Cause: err}
	}
	searched := false
	if search {
		if err := client.SearchSeason(ctx, sonarrID, seasonNumber); err != nil {
			return MonitorSeasonResult{}, &sharedErrors.SonarrUnreachableError{Instance: instanceName, Cause: err}
		}
		searched = true
	}
	uc.d.Logger.InfoContext(ctx, "season_monitor_requested",
		slog.String("instance", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrID)),
		slog.Int("season", seasonNumber),
		slog.Bool("searched", searched))
	return MonitorSeasonResult{SonarrSeriesID: sonarrID, SeasonNumber: seasonNumber, Monitored: true, Searched: searched}, nil
}
