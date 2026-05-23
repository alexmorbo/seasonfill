package decision

import (
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/release"
)

type Outcome string

const (
	OutcomeGrab  Outcome = "grab"
	OutcomeSkip  Outcome = "skip"
	OutcomeError Outcome = "error"
)

type FilteredCandidate struct {
	GUID       string
	Title      string
	Indexer    string
	Reason     string
	Quality    string
	Coverage   int
	Rejections []string
}

// Decision is the per-(scan, series, season) audit row.
//
// DryRunWouldGrab is the dry-run flag answering "would seasonfill have
// grabbed this candidate?". It is set to true only on the dry-run path
// where no POST /release was issued. On the real-grab path it stays
// false because the grab either succeeded (recorded in grab_records)
// or failed (recorded with status=grab_failed in grab_records and a
// guid cooldown row). Renaming the field from the original `WouldGrab`
// removes the historical confusion where the flag looked like a
// post-grab outcome but was actually only ever set on the dry-run
// branch (closes deferred-item #7 in 02-phase2-deltas.md).
type Decision struct {
	ID              uuid.UUID
	ScanRunID       uuid.UUID
	InstanceName    string
	SeriesID        int
	SeriesTitle     string
	SeasonNumber    int
	Outcome         Outcome
	Reason          Reason
	MissingCount    int
	ExistingCount   int
	ReleasesFound   int
	CandidatesCount int
	FilteredOut     []FilteredCandidate
	Selected        *release.Scored
	DryRunWouldGrab bool
	// ErrorDetail is the underlying error string from the upstream
	// failure (e.g. "sonarr: 503 service unavailable") when Outcome is
	// OutcomeError. Empty on non-error decisions. Caller is responsible
	// for size/normalisation; see application/evaluate.truncateErrorDetail.
	ErrorDetail string
	CreatedAt   time.Time
}

func New(scanRunID uuid.UUID, instance, seriesTitle string, seriesID, season int) Decision {
	return Decision{
		ID:           uuid.New(),
		ScanRunID:    scanRunID,
		InstanceName: instance,
		SeriesID:     seriesID,
		SeriesTitle:  seriesTitle,
		SeasonNumber: season,
		CreatedAt:    time.Now().UTC(),
	}
}
