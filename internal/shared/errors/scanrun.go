package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ScanRunNotFoundError signals a missing scan_runs row by id. Maps to
// HTTP 404. Distinct from ScanFailedError (500, retriable) and
// ScanInProgressError (409).
type ScanRunNotFoundError struct {
	ID int64
}

func (e *ScanRunNotFoundError) Error() string { return fmt.Sprintf("scan_run %d not found", e.ID) }

func (e *ScanRunNotFoundError) Code() string { return "scan_run_not_found" }

func (e *ScanRunNotFoundError) Retriable() bool { return false }

// DecisionNotFoundError signals a missing decision row keyed on
// (instance_name, sonarr_series_id, season_number). Maps to HTTP 404.
type DecisionNotFoundError struct {
	InstanceName   domain.InstanceName
	SonarrSeriesID domain.SonarrSeriesID
	SeasonNumber   int
}

func (e *DecisionNotFoundError) Error() string {
	return fmt.Sprintf("decision %s/%d/s%02d not found",
		e.InstanceName, e.SonarrSeriesID, e.SeasonNumber)
}

func (e *DecisionNotFoundError) Code() string { return "decision_not_found" }

func (e *DecisionNotFoundError) Retriable() bool { return false }

// WatchdogBlacklistNotFoundError signals a missing regrab_blacklist row
// by primary key. Maps to HTTP 404 (DELETE endpoint).
type WatchdogBlacklistNotFoundError struct {
	ID uint
}

func (e *WatchdogBlacklistNotFoundError) Error() string {
	return fmt.Sprintf("watchdog_blacklist %d not found", e.ID)
}

func (e *WatchdogBlacklistNotFoundError) Code() string { return "watchdog_blacklist_not_found" }

func (e *WatchdogBlacklistNotFoundError) Retriable() bool { return false }
