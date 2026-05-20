package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
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

type GrabRepository interface {
	Create(ctx context.Context, rec grab.Record) error
}

type CooldownRepository interface {
	Set(ctx context.Context, c cooldown.Cooldown) error
	Get(ctx context.Context, scope cooldown.Scope, key string) (cooldown.Cooldown, bool, error)
	FilterActive(ctx context.Context, scope cooldown.Scope, keys []string, now time.Time) ([]cooldown.Cooldown, error)
	Sweep(ctx context.Context, now time.Time) (int64, error)
}

type OriginRelease struct {
	InstanceName string
	SeriesID     int
	SeasonNumber int
	GUID         string
	IndexerID    int
	IndexerName  string
	Source       string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	LastUsedAt   *time.Time
}

type OriginReleaseRepository interface {
	Get(ctx context.Context, instance string, seriesID, season int) (OriginRelease, bool, error)
	Upsert(ctx context.Context, rec OriginRelease) error
}
