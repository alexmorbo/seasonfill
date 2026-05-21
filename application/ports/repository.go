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
	CreatedAt       time.Time
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

// ScanFilter narrows ScanRepository.List. Pointer fields; nil = no filter.
// From is inclusive, To is exclusive (created_at < To).
type ScanFilter struct {
	Instance *string
	Status   *string
	From     *time.Time
	To       *time.Time
}

type ScanRepository interface {
	Create(ctx context.Context, rec ScanRecord) error
	Update(ctx context.Context, rec ScanRecord) error
	GetByID(ctx context.Context, id uuid.UUID) (ScanRecord, error)
	MarkAborted(ctx context.Context, id uuid.UUID, reason string) error
	List(ctx context.Context, f ScanFilter, p Pagination) ([]ScanRecord, *Cursor, error)
}

type DecisionFilter struct {
	ScanRunID    *uuid.UUID
	Instance     *string
	SeriesID     *int
	SeasonNumber *int
	Decision     *string
	From         *time.Time
	To           *time.Time
}

type DecisionRepository interface {
	Save(ctx context.Context, d decision.Decision) error
	List(ctx context.Context, f DecisionFilter, p Pagination) ([]decision.Decision, *Cursor, error)
}

type GrabFilter struct {
	Instance     *string
	SeriesID     *int
	SeasonNumber *int
	Status       *string
	From         *time.Time
	To           *time.Time
}

type GrabRepository interface {
	Create(ctx context.Context, rec grab.Record) error
	List(ctx context.Context, f GrabFilter, p Pagination) ([]grab.Record, *Cursor, error)
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

// Transactor scopes a unit of work to a single DB transaction. Implementations
// commit if fn returns nil, otherwise rollback. M-7: the grab success path
// uses this to ensure `grabs.Create` + `cooldowns.Set` + `origins.Upsert`
// either all land or none of them do.
//
// fn receives a derived context that carries the tx-scoped DB handle so
// repositories can route writes through the transaction on any SQL backend
// (including Postgres where auto-commit would otherwise break atomicity).
type Transactor interface {
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

type OriginReleaseRepository interface {
	Get(ctx context.Context, instance string, seriesID, season int) (OriginRelease, bool, error)
	Upsert(ctx context.Context, rec OriginRelease) error
}
