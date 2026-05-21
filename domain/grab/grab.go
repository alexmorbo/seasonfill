package grab

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidStatusTransition signals a forbidden grab_records status move.
// Wrap with %w when adding context.
var ErrInvalidStatusTransition = errors.New("invalid grab status transition")

type Status string

const (
	StatusGrabbed      Status = "grabbed"
	StatusGrabFailed   Status = "grab_failed"
	StatusImported     Status = "imported"
	StatusImportFailed Status = "import_failed"
)

// IsTerminal reports whether the status is a final outcome.
func (s Status) IsTerminal() bool {
	switch s {
	case StatusImported, StatusImportFailed, StatusGrabFailed:
		return true
	default:
		return false
	}
}

// CanTransitionTo reports whether `s -> next` is permitted.
//
//   - grabbed -> grabbed | imported | import_failed   (allowed)
//   - imported / import_failed / grab_failed -> *     (forbidden, terminal)
//
// Self-transition for `grabbed` is intentional — duplicate Sonarr Grab
// webhook is a no-op, not an error.
func (s Status) CanTransitionTo(next Status) bool {
	if s.IsTerminal() {
		return false
	}
	switch next {
	case StatusGrabbed, StatusImported, StatusImportFailed:
		return true
	default:
		return false
	}
}

// Record is the persisted shape of one force-grab attempt.
type Record struct {
	ID                uuid.UUID
	InstanceName      string
	SeriesID          int
	SeriesTitle       string
	SeasonNumber      int
	ReleaseGUID       string
	ReleaseTitle      string
	IndexerID         int
	IndexerName       string
	CustomFormatScore int
	Quality           string
	CoverageCount     int
	Status            Status
	ErrorMessage      string
	ScanRunID         uuid.UUID
	Attempts          int
	// DownloadID — Sonarr's download-client hash. Empty until Phase 2
	// grab paths plumb it through (deferred); MatchLatest falls back
	// to (release_title, series_id, season, instance) for legacy rows.
	DownloadID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
