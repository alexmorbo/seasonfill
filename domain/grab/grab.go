package grab

import (
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusGrabbed      Status = "grabbed"
	StatusGrabFailed   Status = "grab_failed"
	StatusImported     Status = "imported"      // future, Phase 4
	StatusImportFailed Status = "import_failed" // future, Phase 4
)

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
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
