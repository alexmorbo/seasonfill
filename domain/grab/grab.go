package grab

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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
	InstanceName      domain.InstanceName
	SeriesID          domain.SonarrSeriesID
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
	// TorrentHash — qBit infohash (40-char lowercase hex), populated
	// from the OnGrab webhook downloadId or the Sonarr force-grab
	// response when valid. nil = pre-Phase-10 row or malformed hash;
	// the Phase 10 Watchdog ignores nil-hash rows (D63 hash-required
	// gate, no backfill).
	TorrentHash *string
	// ReplayOfID is the audit pointer set by the Watchdog regrab use
	// case (039f-2) when a row was written as a re-grab of an earlier
	// row. nil for ordinary scan / rescan / manual paths. Lets the UI
	// (future Phase 11) render a "replay of <original-id>" badge and
	// gives operators a join key for troubleshooting.
	ReplayOfID *uuid.UUID
	// SizeBytes is Sonarr's release.size persisted on insert (043b,
	// Phase 12). nil = unknown — pre-Phase-12 row OR a Sonarr payload
	// that omitted the field. int64 covers ≥ 9 exabytes. The pointer
	// makes "0 bytes" distinguishable from "absent"; Sonarr never
	// emits zero on a real release but the explicit distinction
	// matches the omitempty wire contract.
	SizeBytes *int64
	// Parsed holds the Sonarr /api/v3/parse-derived metadata captured
	// by the OnGrab webhook in 044b. nil = absent (pre-B2 row, parse
	// skipped, or parse failed). Non-nil zero-valued Parsed = parse
	// ran and returned nothing useful — distinct from absent.
	Parsed *Parsed
	// ParsedAt is the wall-clock at which ParseRelease finished
	// successfully (regardless of whether Parsed is zero-valued).
	// Used by the future re-parse CLI (044b) to find rows that never
	// completed a parse pass.
	ParsedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ParseTorrentHash validates and normalises a candidate qBit info-hash
// from a Sonarr webhook downloadId or force-grab response. Returns a
// pointer to the lowercased 40-char hex string on success, or nil on
// any rejection (empty, wrong length, non-hex). Never returns an error
// — malformed input is a normal, silent NULL-write.
func ParseTorrentHash(downloadID string) *string {
	s := strings.TrimSpace(downloadID)
	if len(s) != 40 {
		return nil
	}
	lower := strings.ToLower(s)
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return nil
		}
	}
	return &lower
}
