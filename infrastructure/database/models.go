package database

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

type ScanRunModel struct {
	ID              string    `gorm:"primaryKey;size:36;index:idx_scan_runs_created_at_id,priority:2"`
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
	CreatedAt       time.Time `gorm:"index:idx_scan_runs_created_at_id,priority:1"`
	UpdatedAt       time.Time
}

func (ScanRunModel) TableName() string { return "scan_runs" }

type DecisionModel struct {
	ID              string `gorm:"primaryKey;size:36;index:idx_decisions_created_at_id,priority:2"`
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
	DryRunWouldGrab bool      `gorm:"column:would_grab"`
	CreatedAt       time.Time `gorm:"index:idx_decisions_created_at_id,priority:1"`
}

func (DecisionModel) TableName() string { return "decisions" }

type GrabRecordModel struct {
	ID                string `gorm:"primaryKey;size:36;index:idx_grab_records_created_at_id,priority:2"`
	InstanceName      string `gorm:"size:128;index:idx_grab_inst_series,priority:1"`
	SeriesID          int    `gorm:"index:idx_grab_inst_series,priority:2"`
	SeriesTitle       string `gorm:"size:512"`
	SeasonNumber      int    `gorm:"index:idx_grab_inst_series,priority:3"`
	ReleaseGUID       string `gorm:"size:512;index"`
	ReleaseTitle      string `gorm:"size:1024"`
	IndexerID         int
	IndexerName       string `gorm:"size:256"`
	CustomFormatScore int
	Quality           string `gorm:"size:128"`
	CoverageCount     int
	Status            string `gorm:"size:32;index"`
	ErrorMessage      string `gorm:"type:text"`
	ScanRunID         string `gorm:"size:36;index"`
	Attempts          int
	CreatedAt         time.Time `gorm:"index:idx_grab_records_created_at_id,priority:1"`
	UpdatedAt         time.Time
}

func (GrabRecordModel) TableName() string { return "grab_records" }

type OriginReleaseModel struct {
	InstanceName string `gorm:"primaryKey;size:128"`
	SeriesID     int    `gorm:"primaryKey"`
	SeasonNumber int    `gorm:"primaryKey"`
	GUID         string `gorm:"size:512"`
	IndexerID    int
	IndexerName  string `gorm:"size:256"`
	Source       string `gorm:"size:32"`
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	LastUsedAt   *time.Time
}

func (OriginReleaseModel) TableName() string { return "origin_releases" }

type CooldownModel struct {
	Scope     string    `gorm:"primaryKey;size:16"`
	Key       string    `gorm:"primaryKey;size:512"`
	ExpiresAt time.Time `gorm:"index"`
	Reason    string    `gorm:"size:128"`
	CreatedAt time.Time
}

func (CooldownModel) TableName() string { return "cooldowns" }

func NewScanID() string { return uuid.New().String() }
