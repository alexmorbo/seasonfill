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
	WouldGrab       bool
	CreatedAt       time.Time
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
