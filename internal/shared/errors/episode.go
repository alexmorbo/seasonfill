package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// EpisodeNotFoundError signals a missing episode row. Maps to HTTP 404.
type EpisodeNotFoundError struct {
	ID domain.EpisodeID
}

func (e *EpisodeNotFoundError) Error() string {
	return fmt.Sprintf("episode %d not found", e.ID)
}

func (e *EpisodeNotFoundError) Code() string { return "episode_not_found" }

func (e *EpisodeNotFoundError) Retriable() bool { return false }

// SeasonNotFoundError signals a missing season row keyed on
// (instance_name, sonarr_series_id, season_number). Maps to HTTP 404.
type SeasonNotFoundError struct {
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeasonNumber   int
}

func (e *SeasonNotFoundError) Error() string {
	return fmt.Sprintf("season %s/%d/s%02d not found",
		e.InstanceName, e.SonarrSeriesID, e.SeasonNumber)
}

func (e *SeasonNotFoundError) Code() string { return "season_not_found" }

func (e *SeasonNotFoundError) Retriable() bool { return false }
