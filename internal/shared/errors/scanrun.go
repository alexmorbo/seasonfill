package errors

import (
	"fmt"

	"github.com/google/uuid"
)

// ScanRunNotFoundError signals a missing scan_runs row by id. Maps to
// HTTP 404. Distinct from ScanFailedError (500, retriable) and
// ScanInProgressError (409). The id is the uuid.UUID primary key — the
// scan_runs table is keyed on a UUID string, not an int64.
type ScanRunNotFoundError struct {
	ID uuid.UUID
}

func (e *ScanRunNotFoundError) Error() string {
	return fmt.Sprintf("scan_run %q not found", e.ID)
}

func (e *ScanRunNotFoundError) Code() string { return "scan_run_not_found" }

func (e *ScanRunNotFoundError) Retriable() bool { return false }

// DecisionNotFoundError signals a missing decisions row by primary key.
// Maps to HTTP 404. The decision_repository looks rows up by uuid.UUID
// only; there is no compound (instance/series/season) lookup path on
// the read side.
type DecisionNotFoundError struct {
	ID uuid.UUID
}

func (e *DecisionNotFoundError) Error() string {
	return fmt.Sprintf("decision %q not found", e.ID)
}

func (e *DecisionNotFoundError) Code() string { return "decision_not_found" }

func (e *DecisionNotFoundError) Retriable() bool { return false }

// WatchdogBlacklistNotFoundError signals a missing watchdog_blacklist
// row. D-1 / 467b: composite PK on (instance_name, sonarr_series_id,
// season_number). Maps to HTTP 404 (DELETE endpoint).
type WatchdogBlacklistNotFoundError struct {
	Instance string
	SeriesID int64
	Season   int
}

func (e *WatchdogBlacklistNotFoundError) Error() string {
	return fmt.Sprintf("watchdog_blacklist (%s, %d, %d) not found",
		e.Instance, e.SeriesID, e.Season)
}

func (e *WatchdogBlacklistNotFoundError) Code() string { return "watchdog_blacklist_not_found" }

func (e *WatchdogBlacklistNotFoundError) Retriable() bool { return false }
