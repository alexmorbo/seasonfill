package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/regrab"
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
	// IncrementSeriesScanned atomically adds `by` to the persisted
	// series_scanned counter for the row. Used by the in-progress
	// scan goroutine so `GET /scans/{id}` reflects live progress
	// without rewriting every counter field. ErrNotFound on unknown id.
	IncrementSeriesScanned(ctx context.Context, id uuid.UUID, by int) error
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
	GetByID(ctx context.Context, id uuid.UUID) (decision.Decision, error)
	// UpdateSupersededBy: ports.ErrNotFound on unknown id.
	UpdateSupersededBy(ctx context.Context, id, newID uuid.UUID) error
	// ClearSupersededBy resets superseded_by_id to NULL. Used by the
	// async rescan rollback when the goroutine fails after the
	// prelude already pre-applied the supersede pointer.
	// ports.ErrNotFound on unknown id.
	ClearSupersededBy(ctx context.Context, id uuid.UUID) error
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

// MatchKey selects one grab_records row for a webhook event. Precedence (R-3):
//
//  1. DownloadID alone, when non-empty.
//  2. Fallback tuple (ReleaseTitle, SeriesID, SeasonNumber, InstanceName)
//     when DownloadID is empty OR step 1 finds nothing.
//
// Both queries exclude terminal rows (imported / import_failed /
// grab_failed) — duplicate Sonarr deliveries cannot rewrite settled state.
type MatchKey struct {
	DownloadID   string
	ReleaseTitle string
	SeriesID     int
	SeasonNumber int
	InstanceName string
}

type GrabRepository interface {
	Create(ctx context.Context, rec grab.Record) error
	List(ctx context.Context, f GrabFilter, p Pagination) ([]grab.Record, *Cursor, error)

	// MatchLatest implements the MatchKey precedence rule. Returns
	// ErrNotFound when neither key path resolves a non-terminal row.
	MatchLatest(ctx context.Context, key MatchKey) (grab.Record, error)

	// UpdateStatus writes status + message + updated_at on the row.
	// Returns ErrNotFound on unknown id and grab.ErrInvalidStatusTransition
	// when the persisted status forbids the move.
	UpdateStatus(ctx context.Context, id uuid.UUID, newStatus grab.Status, message string) error
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

// QbitSettingsRecord is the transport shape for instance_qbit_settings.
// PasswordEncrypted carries the AES-GCM payload opaquely — the repo
// neither encrypts nor decrypts; that responsibility lives in the 039d
// HTTP handler. CustomUnregisteredMsgs is a free-form string slice that
// the JSON column on the DB side accepts as a JSON array.
type QbitSettingsRecord struct {
	ID                     uint
	InstanceID             uint
	Enabled                bool
	URL                    string
	Username               *string
	PasswordEncrypted      []byte
	Category               string
	PollIntervalMinutes    int
	RegrabCooldownHours    int
	MaxConsecutiveNoBetter int
	CustomUnregisteredMsgs []string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// QbitSettingsRepository persists the per-instance Watchdog configuration.
// Upsert is keyed on InstanceID (one settings row per Sonarr instance,
// enforced by a unique index). GetByInstance / DeleteByInstance look up
// by the foreign instance id. ports.ErrNotFound on miss.
type QbitSettingsRepository interface {
	Upsert(ctx context.Context, rec QbitSettingsRecord) error
	GetByInstance(ctx context.Context, instanceID uint) (QbitSettingsRecord, error)
	DeleteByInstance(ctx context.Context, instanceID uint) error
	List(ctx context.Context) ([]QbitSettingsRecord, error)
}

// WatchdogBlacklistFilter narrows ListByInstance reads when needed. For
// 039a only InstanceID is supported; the regrab use case (039f) extends
// this shape as it grows new query needs.
type WatchdogBlacklistFilter struct {
	InstanceID uint
}

// WatchdogBlacklistRepository persists the parked (instance, series,
// season) triples. Upsert is keyed on the triple unique index; a repeat
// Upsert on the same triple overwrites the prior Consecutive counter
// and CreatedAt (the latest detection cycle's bookkeeping wins).
type WatchdogBlacklistRepository interface {
	// Find returns the row matching (instance, series, season) exactly.
	// ports.ErrNotFound on miss.
	Find(ctx context.Context, instanceID uint, seriesID, season int) (regrab.BlacklistEntry, error)

	// Upsert writes the row keyed on (instance, series, season). On
	// conflict, Consecutive / Reason / CreatedAt / ExpiresAt are
	// replaced with the supplied values.
	Upsert(ctx context.Context, entry regrab.BlacklistEntry) error

	// DeleteByTriple removes the parked row. ports.ErrNotFound on miss.
	DeleteByTriple(ctx context.Context, instanceID uint, seriesID, season int) error

	// ListByInstance returns every parked row for the instance. Used by
	// the metrics gauge `seasonfill_watchdog_blacklist_size{instance}`.
	ListByInstance(ctx context.Context, instanceID uint) ([]regrab.BlacklistEntry, error)
}
