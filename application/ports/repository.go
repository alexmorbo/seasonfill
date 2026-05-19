package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/decision"
)

type ScanRecord struct {
	ID              uuid.UUID
	InstanceName    string
	Trigger         string
	StartedAt       time.Time
	FinishedAt      *time.Time
	Status          string
	SeriesScanned   int
	CandidatesFound int
	GrabsPerformed  int
	GrabsFailed     int
	ErrorsCount     int
	ErrorMessage    string
	DryRun          bool
}

type ScanRepository interface {
	Create(ctx context.Context, rec ScanRecord) error
	Update(ctx context.Context, rec ScanRecord) error
	GetByID(ctx context.Context, id uuid.UUID) (ScanRecord, error)
	MarkAborted(ctx context.Context, id uuid.UUID, reason string) error
}

type DecisionRepository interface {
	Save(ctx context.Context, d decision.Decision) error
}
