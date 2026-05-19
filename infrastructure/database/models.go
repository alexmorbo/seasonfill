package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type ScanRunModel struct {
	ID              string    `gorm:"primaryKey;size:36"`
	InstanceName    string    `gorm:"size:128;index"`
	Trigger         string    `gorm:"size:32"`
	StartedAt       time.Time `gorm:"index"`
	FinishedAt      *time.Time
	Status          string `gorm:"size:32"`
	SeriesScanned   int
	CandidatesFound int
	GrabsPerformed  int
	GrabsFailed     int
	ErrorsCount     int
	ErrorMessage    string `gorm:"type:text"`
	DryRun          bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (ScanRunModel) TableName() string { return "scan_runs" }

type DecisionModel struct {
	ID              string `gorm:"primaryKey;size:36"`
	ScanRunID       string `gorm:"size:36;index"`
	InstanceName    string `gorm:"size:128;index"`
	SeriesID        int    `gorm:"index"`
	SeriesTitle     string `gorm:"size:512"`
	SeasonNumber    int
	Decision        string `gorm:"size:32"`
	Reason          string `gorm:"size:128"`
	MissingCount    int
	ExistingCount   int
	ReleasesFound   int
	CandidatesCount int
	FilteredOut     datatypes.JSON
	SelectedGUID    string `gorm:"size:512"`
	SelectedData    datatypes.JSON
	WouldGrab       bool
	CreatedAt       time.Time
}

func (DecisionModel) TableName() string { return "decisions" }

func NewScanID() string { return uuid.New().String() }
