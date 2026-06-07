package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/regrab"
	"github.com/alexmorbo/seasonfill/domain/series"
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

	// UpdateTorrentHash writes the qBit info-hash onto an existing
	// grab_records row when its torrent_hash column is currently NULL.
	// Idempotent: a row that already has a non-NULL hash returns nil
	// without overwriting (D63 hash-required gate — never rewrite a
	// hash captured by an earlier OnGrab delivery or grab-time
	// parse). Returns ErrNotFound when the row id is unknown.
	// hash is expected to already be 40-char lowercase hex (the caller
	// runs grab.ParseTorrentHash); an empty hash argument is a no-op
	// success (defensive).
	UpdateTorrentHash(ctx context.Context, id uuid.UUID, hash string) error

	// FindLatestSuccessByHash returns the newest grab_records row whose
	// torrent_hash matches the supplied 40-char lowercase hex value AND
	// whose status is NOT grab_failed. Used by the Phase 10 Watchdog
	// regrab use case to map qBit infohash → (instance, series, season).
	// hash is normalised lowercase by the caller. Returns ErrNotFound
	// when no matching row exists. Empty hash returns ErrNotFound
	// directly (defensive — never SELECT WHERE torrent_hash = '').
	FindLatestSuccessByHash(ctx context.Context, hash string) (grab.Record, error)

	// CreateReplay writes a new grab_records row with ReplayOfID set
	// to the supplied uuid. Otherwise identical to Create — same
	// uniqueness contract (uuid PK), same UpdatedAt path. The
	// repository implementation funnels through the same INSERT used
	// by Create; this method exists so callers don't have to mutate
	// rec.ReplayOfID before calling Create (clearer intent at the
	// call site).
	CreateReplay(ctx context.Context, rec grab.Record, replayOfID uuid.UUID) error

	// SetReplayOfID stamps the replay_of_id column on an existing row.
	// Idempotent: re-stamping the same id is a no-op success. Returns
	// ErrNotFound on unknown row id. Used by the Watchdog regrab use
	// case (039f-2) to attach the audit pointer AFTER the grab use
	// case has persisted the row (grab.UseCase doesn't know about
	// the regrab audit pointer; this is the cleanest separation).
	SetReplayOfID(ctx context.Context, id uuid.UUID, replayOfID uuid.UUID) error

	// ListReplaysOf returns the children of each parent id — the
	// reverse of replay_of_id. Result map key = parent uuid; value =
	// child grab_records ids that point at that parent. Ordered
	// newest-first by created_at; capped at MaxReplaysPerParent per
	// PRD §9 risk #7. Parents with no children are absent from the
	// map. Empty parentIDs yields an empty map without a SQL call.
	// 043a: powers the Grab DTO `replayed_by` derived field.
	// One SQL round-trip regardless of page size.
	ListReplaysOf(ctx context.Context, parentIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)

	// UpdateSizeBytes writes size_bytes when currently NULL.
	// Idempotent: non-null returns nil without overwriting. Returns
	// ErrNotFound on unknown id. size <= 0 is a no-op success (Sonarr
	// omits release.size sometimes; we never persist 0 B).
	// 043b: stamped by the OnGrab webhook use case.
	UpdateSizeBytes(ctx context.Context, id uuid.UUID, size int64) error

	// GetByID returns the grab_records row matching the supplied uuid.
	// Returns ErrNotFound on miss. 043c: powers the episode-files
	// endpoint lookup (handler reads instance_name + status from the
	// returned row before calling Sonarr).
	GetByID(ctx context.Context, id uuid.UUID) (grab.Record, error)

	// CountReplaysSince — count of grab_records rows for instanceName
	// whose replay_of_id IS NOT NULL AND created_at >= since.
	CountReplaysSince(ctx context.Context, instanceName string, since time.Time) (int, error)

	// CountReplaysAll — lifetime count of replays for instanceName.
	CountReplaysAll(ctx context.Context, instanceName string) (int, error)
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

	// CountByInstance — current row count for instanceID. Zero on no rows.
	CountByInstance(ctx context.Context, instanceID uint) (int, error)
}

// NoBetterCounterRepository persists the live consecutive-no-better
// counters per (instance, series, season). The regrab use case uses
// Get → (if not found) Insert → Increment cycle per detection, and
// Reset when the counter is escalated to the blacklist.
//
// Increment is atomic against concurrent regrab loops: the repository
// implementation MUST use an UPSERT (INSERT … ON CONFLICT DO UPDATE)
// so two parallel polls on the same triple cannot both stamp
// consecutive=1 — the second wins and observes consecutive=2.
type NoBetterCounterRepository interface {
	// Get returns the counter for the triple. ports.ErrNotFound on miss
	// — the use case treats that as "fresh triple, insert" via
	// Increment(now=current).
	Get(ctx context.Context, instanceID uint, seriesID, season int) (regrab.NoBetterCounter, error)

	// Increment atomically bumps consecutive by 1 (or inserts a row
	// with consecutive=1 on first contact). Returns the post-increment
	// counter so the use case can decide whether to escalate.
	Increment(ctx context.Context, instanceID uint, seriesID, season int, now time.Time) (regrab.NoBetterCounter, error)

	// Reset zeros consecutive on the row. ports.ErrNotFound when no
	// row exists — the use case treats that as a non-error path
	// because "nothing to reset" is fine after a fresh insert.
	Reset(ctx context.Context, instanceID uint, seriesID, season int, now time.Time) error

	// DeleteByTriple removes the row entirely (used when an instance's
	// settings are deleted via 039d Delete). ports.ErrNotFound on miss.
	DeleteByTriple(ctx context.Context, instanceID uint, seriesID, season int) error
}

// SeriesCacheRepository persists the per-instance Sonarr series cache
// (D66). Get returns soft-deleted rows too (the webhook SeriesAdd
// handler in 041f reuses the row to preserve the historical poster_path
// when a series is re-added). ListActiveByInstance is the read path
// for the queue/scan handlers and skips soft-deleted rows. SoftDelete
// sets deleted_at to now; the row is never physically removed in
// normal operation (cascade happens application-side in
// SonarrInstanceRepository.Delete, extended in 041e).
//
// The implementation lives in 041e — this story declares the contract
// only so the planning of 041a/041b stays decoupled from cache work.
type SeriesCacheRepository interface {
	// Get returns the cache row matching (instance_name, sonarr_series_id)
	// regardless of soft-delete state. ports.ErrNotFound on miss.
	Get(ctx context.Context, instanceName string, sonarrSeriesID int) (series.CacheEntry, error)

	// Upsert writes or replaces the row keyed on the composite PK. If
	// the entry's DeletedAt is non-nil the row is stored as soft-deleted;
	// callers that mean "resurrect" should set DeletedAt = nil and
	// refresh UpdatedAt to now.
	Upsert(ctx context.Context, entry series.CacheEntry) error

	// SoftDelete sets deleted_at to now on the matching row.
	// ports.ErrNotFound on miss.
	SoftDelete(ctx context.Context, instanceName string, sonarrSeriesID int) error

	// ListActiveByInstance returns every non-soft-deleted row for the
	// instance. Used by the queue handler (041g) to join cache metadata
	// onto live queue items.
	ListActiveByInstance(ctx context.Context, instanceName string) ([]series.CacheEntry, error)

	// ListByFilter returns active (non-soft-deleted) cache rows for an
	// instance, narrowed by SeriesCacheFilter, sorted per
	// SeriesCacheSort, and keyset-paginated by Pagination. Third return
	// is the pre-limit total. Fourth return is hasMore: true when an
	// additional page exists. Limit is clamped to MaxListLimit at the
	// repo edge; the HTTP edge (045b) clamps tighter.
	ListByFilter(
		ctx context.Context,
		instanceName string,
		filter SeriesCacheFilter,
		sort SeriesCacheSort,
		page Pagination,
	) (items []series.CacheEntry, total int, hasMore bool, next *Cursor, err error)

	// FetchLastGrabInfo aggregates the latest imported grab_records row per
	// series id in ONE query (defence against N+1). Returns a map keyed on
	// series id; missing ids map to zero-value LastGrabInfo (empty time +
	// empty string).
	FetchLastGrabInfo(
		ctx context.Context, instanceName string, seriesIDs []int,
	) (map[int]LastGrabInfo, error)
}

// SeriesCacheState narrows the series_cache list by derived membership.
// Imported = the series received an "imported" grab_records row in the
// last 7 days. Missing = the series has a non-zero cached missing_count.
// All = no narrowing.
type SeriesCacheState string

const (
	SeriesCacheStateAll      SeriesCacheState = "all"
	SeriesCacheStateImported SeriesCacheState = "imported"
	SeriesCacheStateMissing  SeriesCacheState = "missing"
)

// IsValid reports whether the state is one of the three known values.
func (s SeriesCacheState) IsValid() bool {
	switch s {
	case SeriesCacheStateAll, SeriesCacheStateImported, SeriesCacheStateMissing:
		return true
	}
	return false
}

// SeriesCacheSort selects the ORDER BY clause for ListByFilter. The
// keyset cursor encoding switches on sort: UpdatedDesc encodes
// (updated_at, sonarr_series_id); TitleAsc encodes
// ("lower(title)|sonarr_series_id" packed into the cursor ID slot).
type SeriesCacheSort string

const (
	SeriesCacheSortUpdatedDesc SeriesCacheSort = "updated_desc"
	SeriesCacheSortTitleAsc    SeriesCacheSort = "title_asc"
)

// IsValid reports whether the sort key is one of the supported values.
func (s SeriesCacheSort) IsValid() bool {
	switch s {
	case SeriesCacheSortUpdatedDesc, SeriesCacheSortTitleAsc:
		return true
	}
	return false
}

// SeriesCacheFilter aggregates the optional narrowing predicates for
// ListByFilter. Only State drives the WHERE today; the struct is the
// extension point for later (q-prefix, monitored, year-range).
type SeriesCacheFilter struct {
	State SeriesCacheState
}

// LastGrabInfo holds the aggregated grab data for the list endpoint.
// Keyed on (instance_name, sonarr_series_id) at the call site. Empty
// value (zero time + empty string) means "no grab yet".
type LastGrabInfo struct {
	LastGrabAt          time.Time
	LastImportedEpisode string
}
