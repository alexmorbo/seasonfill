package decision

import (
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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
//
// 046a adds Total/Aired/Existing/GrabbedEpisodes — the partial-pack
// counter snapshot computed at decision-write time. MissingCount and
// ExistingCount stay for back-compat (the legacy len(missing)/len(have)
// view); ExistingEpisodes is the canonical new accessor (renames the
// concept without breaking the wire).
type Decision struct {
	ID              uuid.UUID
	ScanRunID       uuid.UUID
	InstanceName    domain.InstanceName
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
	ErrorDetail    string
	SupersededByID *uuid.UUID // nil = live; set by rescan (017)
	// 046a season-stats snapshot. All four default to zero on pre-046a
	// rows (the migration backfills 0 NOT NULL) — UI must tolerate 0 as
	// "unknown". TotalEpisodes/AiredEpisodes/ExistingEpisodes come from
	// Sonarr's per-season statistics block at scan time; GrabbedEpisodes
	// is computed once at decision-persist time via a single count
	// against grab_records (status=imported), locking the value to the
	// scan that produced the decision.
	TotalEpisodes    int
	AiredEpisodes    int
	ExistingEpisodes int
	GrabbedEpisodes  int
	// Intent is the F-P2-2 "why this grab" capture (091a). nil on
	// pre-091a rows AND on paths where the call site couldn't infer
	// a reason (synthetic skip rows, error rows). The DTO emits
	// `null` for nil Intent so the frontend treats absent intent as
	// honest unknown rather than a typed default.
	Intent    *Intent
	CreatedAt time.Time
}

func New(scanRunID uuid.UUID, instance domain.InstanceName, seriesTitle string, seriesID, season int) Decision {
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
