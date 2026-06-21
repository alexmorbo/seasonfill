package dataports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

type ScanRecord struct {
	ID              uuid.UUID
	InstanceName    domain.InstanceName
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
	Instance *domain.InstanceName
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
	Instance     *domain.InstanceName
	SeriesID     *domain.SonarrSeriesID
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
	// UpdateIntent writes the intent JSON column on an existing
	// decision row. Used by the regrab use case (091a / F-P2-2) to
	// refine ChosenBecauseWatchdogBetterOther into
	// ChosenBecauseWatchdogBetterQuality once the candidate's quality
	// is known. A nil intent persists NULL (resets the column).
	// ports.ErrNotFound on unknown id.
	UpdateIntent(ctx context.Context, id uuid.UUID, intent *decision.Intent) error
	List(ctx context.Context, f DecisionFilter, p Pagination) ([]decision.Decision, *Cursor, error)
}

type GrabFilter struct {
	Instance     *domain.InstanceName
	SeriesID     *domain.SonarrSeriesID
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
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
	InstanceName domain.InstanceName
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
	// runs grab.ParseTorrentHash, which delegates to
	// domain.NewQbitHash for the canonical regex + normalisation);
	// an empty hash argument is a no-op success (defensive).
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

	// ListUnparsedSince returns every grab_records row whose parsed_at
	// IS NULL AND created_at >= since. Newest first. Cap defaults to
	// 1000 rows — callers should page if they expect more.
	ListUnparsedSince(ctx context.Context, since time.Time, limit int) ([]grab.Record, error)

	// UpdateParsed writes the parsed_* columns + parsed_at on the row.
	// Idempotent: re-writing the same payload is harmless. Returns
	// ErrNotFound on unknown id. A nil parsed argument writes NULLs
	// (and a non-nil parsedAt) — useful for "parse ran but returned
	// nothing" records.
	UpdateParsed(ctx context.Context, id uuid.UUID, parsed *grab.Parsed, parsedAt time.Time) error

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

	// CountImportedEpisodes returns the number of distinct grab_records
	// rows for the (instance, series, season) triple with status =
	// "imported". 046a uses this to snapshot the GrabbedEpisodes counter
	// onto every new Decision at write time, so historical decisions
	// don't shift under future grabs (the count is locked to the moment
	// the decision was made). A zero return on a missing-triple query
	// is NOT an error — it is the normal "this scan has never grabbed
	// here" case. Errors should surface only on real DB failures.
	CountImportedEpisodes(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, seasonNumber int) (int, error)

	// GetByID returns the grab_records row matching the supplied uuid.
	// Returns ErrNotFound on miss. 043c: powers the episode-files
	// endpoint lookup (handler reads instance_name + status from the
	// returned row before calling Sonarr).
	GetByID(ctx context.Context, id uuid.UUID) (grab.Record, error)

	// CountReplaysSince — count of grab_records rows for instanceName
	// whose replay_of_id IS NOT NULL AND created_at >= since.
	CountReplaysSince(ctx context.Context, instanceName domain.InstanceName, since time.Time) (int, error)

	// CountReplaysAll — lifetime count of replays for instanceName.
	CountReplaysAll(ctx context.Context, instanceName domain.InstanceName) (int, error)
}

type CooldownRepository interface {
	Set(ctx context.Context, c cooldown.Cooldown) error
	Get(ctx context.Context, scope cooldown.Scope, key string) (cooldown.Cooldown, bool, error)
	FilterActive(ctx context.Context, scope cooldown.Scope, keys []string, now time.Time) ([]cooldown.Cooldown, error)
	Sweep(ctx context.Context, now time.Time) (int64, error)
}

type OriginRelease struct {
	InstanceName domain.InstanceName
	SeriesID     domain.SonarrSeriesID
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
	Get(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (OriginRelease, bool, error)
	Upsert(ctx context.Context, rec OriginRelease) error
}

// EpisodeGrabRepository persists the per-episode-per-grab projection. The
// repository is co-owned by the grab use case (writes after grab_records
// insert) and the catalog webhook (writes from the OnGrab payload's
// episodes[] array).
//
// 467a / D-6: BatchUpsert is the single insertion path — both callers
// route through it. ListByGrabID powers the /grabs/{id} detail handler;
// ListByEpisodeID powers the future /episodes/{id}/grab-history endpoint.
type EpisodeGrabRepository interface {
	// BatchUpsert inserts (grab_id, episode_id, episode_number) triples
	// atomically. ON CONFLICT (grab_id, episode_id) DO UPDATE updated_at
	// — re-delivering the same webhook is a silent no-op timestamp bump.
	// Empty refs returns nil with zero round-trips.
	BatchUpsert(ctx context.Context, refs []grab.EpisodeRef) error

	// ListByGrabID returns every episode pinned to one grab_records row,
	// ordered by episode_number ASC. Empty result with no error if the
	// grab has no episode fanout yet.
	ListByGrabID(ctx context.Context, grabID string) ([]grab.EpisodeRef, error)

	// ListByEpisodeID returns every grab that touched a specific episode,
	// ordered by created_at DESC. Empty result with no error if no grab
	// has referenced the episode.
	ListByEpisodeID(ctx context.Context, episodeID domain.EpisodeID) ([]grab.EpisodeRef, error)
}

// DownloadLinkRepository persists the qBit-hash → series bridge. PRD §5.4
// Phase 1 covers webhook + arr-poll only; the instance-backfill job lives
// in N-5 scope.
//
// 467a / D-6: InsertOnly is the only write path used by Phase 1 — both
// callers (webhook OnGrab, arr-poll loop) race to populate the same
// qbit_hash row; the silent ON CONFLICT DO NOTHING dedupe lets either
// path win without coordination.
type DownloadLinkRepository interface {
	// InsertOnly inserts a new download_links row. ON CONFLICT (qbit_hash)
	// DO NOTHING — both webhook + arr-poll race to populate the same
	// hash with equivalent payloads, so the silent dedupe is correct.
	// Returns nil even on conflict.
	InsertOnly(ctx context.Context, link grab.DownloadLink) error

	// FindByHash resolves a single download_links row by qbit_hash. Used
	// by the matcher Strategy 1 lookup. Returns ErrNotFound on miss; the
	// caller falls back to fuzzy matching.
	FindByHash(ctx context.Context, hash domain.QbitHash) (grab.DownloadLink, error)

	// SetGlobalSeriesID stamps the canon series_id when enrichment
	// hydrates the foreign series. UPDATE ... WHERE qbit_hash = ? AND
	// global_series_id IS NULL (idempotent — never overwrites a value
	// already set). Returns nil on miss; the row may have been swept
	// while enrichment ran.
	SetGlobalSeriesID(ctx context.Context, hash domain.QbitHash, seriesID domain.SeriesID) error

	// ListByInstance returns the N most-recently-discovered download_links
	// rows for instance, optionally filtered by source. Powers the future
	// /queue UI and the Phase 1 audit listing. limit <= 0 / > MaxListLimit
	// is clamped to MaxListLimit.
	ListByInstance(ctx context.Context, instance domain.InstanceName, source *grab.LinkSource, limit int) ([]grab.DownloadLink, error)
}

// QbitSettingsRecord is the transport shape for qbit_settings.
// PasswordEncrypted carries the AES-GCM payload opaquely — the repo
// neither encrypts nor decrypts; that responsibility lives in the 039d
// HTTP handler. CustomUnregisteredMsgs is a free-form string slice that
// the JSON column on the DB side accepts as a JSON array.
//
// D-6 (story 467c): the row keys on InstanceName (typed
// domain.InstanceName) — the legacy uint InstanceID surrogate was
// dropped when sonarr_instance moved to a TEXT name PK in D-1.
type QbitSettingsRecord struct {
	InstanceName           domain.InstanceName
	Enabled                bool
	URL                    string
	Username               *string
	PasswordEncrypted      []byte
	Category               string
	PollIntervalMinutes    int
	RegrabCooldownHours    int
	MaxConsecutiveNoBetter int
	CustomUnregisteredMsgs []string
	// PublicURL is the optional browser-reachable qBittorrent web UI URL
	// (082, F-P2-1). Empty string = the SPA must fall back to URL. The
	// repo translates NULL ↔ "" so callers see a non-pointer string.
	PublicURL string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// QbitSettingsRepository persists the per-instance Watchdog configuration.
// Upsert is keyed on InstanceName (one settings row per Sonarr instance,
// enforced by the PK on qbit_settings.instance_name). GetByInstance /
// DeleteByInstance look up by the typed name. ports.ErrNotFound on miss.
type QbitSettingsRepository interface {
	Upsert(ctx context.Context, rec QbitSettingsRecord) error
	GetByInstance(ctx context.Context, instance domain.InstanceName) (QbitSettingsRecord, error)
	DeleteByInstance(ctx context.Context, instance domain.InstanceName) error
	List(ctx context.Context) ([]QbitSettingsRecord, error)
}

// WatchdogBlacklistFilter narrows ListByInstance reads when needed.
type WatchdogBlacklistFilter struct {
	InstanceName domain.InstanceName
}

// WatchdogBlacklistRepository persists the parked (instance, series,
// season) triples. D-1 / 467b: composite PK on (instance_name,
// sonarr_series_id, season_number) — no surrogate id. Upsert is keyed
// on the triple; a repeat Upsert on the same triple overwrites the
// prior Consecutive counter, Reason, BlacklistedAt, TTLUntil and
// ReleaseTitle (the latest detection cycle's bookkeeping wins).
type WatchdogBlacklistRepository interface {
	// Find returns the row matching (instance, series, season) exactly.
	// ports.ErrNotFound on miss.
	Find(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (regrab.BlacklistEntry, error)

	// Upsert writes the row keyed on (instance, series, season). On
	// conflict, Reason / Consecutive / BlacklistedAt / TTLUntil /
	// ReleaseTitle are replaced with the supplied values.
	Upsert(ctx context.Context, entry regrab.BlacklistEntry) error

	// DeleteByTriple removes the parked row. ports.ErrNotFound on miss.
	// Replaces legacy DeleteByID — composite PK lookup directly.
	DeleteByTriple(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) error

	// ListByInstance returns every parked row for the instance. Used by
	// the metrics gauge `seasonfill_watchdog_blacklist_size{instance}`.
	ListByInstance(ctx context.Context, instance domain.InstanceName) ([]regrab.BlacklistEntry, error)

	// CountByInstance — current row count for instance. Zero on no rows.
	CountByInstance(ctx context.Context, instance domain.InstanceName) (int, error)

	// ListByInstanceWithLimit returns rows for instance ordered by
	// (blacklisted_at DESC, sonarr_series_id DESC, season_number DESC),
	// capped at limit. Used by the HTTP blacklist list handler.
	// afterBlacklistedAt + afterSeriesID + afterSeason together form
	// the keyset cursor — zero values mean "first page". When the
	// returned slice has len == limit, the caller may issue a follow-up
	// call with the last row's keyset to fetch the next page.
	ListByInstanceWithLimit(
		ctx context.Context,
		instance domain.InstanceName,
		limit int,
		afterBlacklistedAt time.Time,
		afterSeriesID domain.SonarrSeriesID,
		afterSeason int,
	) ([]regrab.BlacklistEntry, error)
}

// WatchdogStateRepository persists the live (instance, series, season)
// regrab tracking row. D-1 / 467b: replaces the legacy
// NoBetterCounterRepository — attempt_count is the consecutive counter;
// cooldown_until + last_error are new D-1 columns (was implicit in loop
// scheduler + logs only).
//
// Increment is atomic against concurrent regrab loops: the repository
// implementation MUST use an UPSERT (INSERT … ON CONFLICT DO UPDATE)
// so two parallel polls on the same triple cannot both stamp
// attempt_count=1 — the second wins and observes attempt_count=2.
type WatchdogStateRepository interface {
	// Get returns the state row for the triple. ports.ErrNotFound on
	// miss — the use case treats that as "fresh triple, insert" via
	// Increment(now=current).
	Get(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (regrab.WatchdogState, error)

	// Increment atomically bumps attempt_count by 1 (or inserts a row
	// with attempt_count=1 on first contact). Returns the post-update
	// row so the use case can decide whether to escalate.
	Increment(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, now time.Time) (regrab.WatchdogState, error)

	// Reset zeros attempt_count on the row. ports.ErrNotFound when no
	// row exists — caller treats that as a non-error path.
	Reset(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, now time.Time) error

	// SetCooldownUntil writes the cooldown_until stamp on the row.
	// ports.ErrNotFound when no row exists (caller must Increment first).
	SetCooldownUntil(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, until time.Time) error

	// SetLastError writes the last_error column on the row.
	// ports.ErrNotFound when no row exists.
	SetLastError(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, errMsg string) error

	// DeleteByTriple removes the row entirely (used when an instance's
	// settings are deleted). ports.ErrNotFound on miss.
	DeleteByTriple(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) error

	// ListByInstance returns every state row for the instance, ordered
	// by updated_at DESC. Powers the /watchdog/state UI rollup.
	ListByInstance(ctx context.Context, instance domain.InstanceName) ([]regrab.WatchdogState, error)

	// ListCooldownsDue returns rows whose cooldown_until <= now (only
	// non-NULL cooldowns are considered), ordered by cooldown_until ASC.
	// Powers the regrab loop scheduler.
	ListCooldownsDue(ctx context.Context, instance domain.InstanceName, now time.Time) ([]regrab.WatchdogState, error)
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
	Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (series.CacheEntry, error)

	// Upsert writes or replaces the row keyed on the composite PK. If
	// the entry's DeletedAt is non-nil the row is stored as soft-deleted;
	// callers that mean "resurrect" should set DeletedAt = nil and
	// refresh UpdatedAt to now.
	Upsert(ctx context.Context, entry series.CacheEntry) error

	// SoftDelete sets deleted_at to now on the matching row.
	// ports.ErrNotFound on miss.
	SoftDelete(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) error

	// ListActiveByInstance returns every non-soft-deleted row for the
	// instance. Used by the queue handler (041g) to join cache metadata
	// onto live queue items.
	ListActiveByInstance(ctx context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error)

	// ListByFilter returns active (non-soft-deleted) cache rows for an
	// instance, narrowed by SeriesCacheFilter, sorted per
	// SeriesCacheSort, and keyset-paginated by Pagination. Third return
	// is the pre-limit total. Fourth return is hasMore: true when an
	// additional page exists. Limit is clamped to MaxListLimit at the
	// repo edge; the HTTP edge (045b) clamps tighter.
	ListByFilter(
		ctx context.Context,
		instanceName domain.InstanceName,
		filter SeriesCacheFilter,
		sort SeriesCacheSort,
		page Pagination,
	) (items []series.CacheEntry, total int, hasMore bool, next *Cursor, err error)

	// FetchLastGrabInfo aggregates the latest imported grab_records row per
	// series id in ONE query (defence against N+1). Returns a map keyed on
	// series id; missing ids map to zero-value LastGrabInfo (empty time +
	// empty string).
	FetchLastGrabInfo(
		ctx context.Context, instanceName domain.InstanceName, seriesIDs []domain.SonarrSeriesID,
	) (map[domain.SonarrSeriesID]LastGrabInfo, error)

	// ListDistinctNetworks returns the sorted, distinct, non-empty
	// network strings present in the instance's active (non-soft-deleted)
	// series_cache rows. Story 121a: the /series networks facet panel
	// needs the full set regardless of which page is loaded. Result is
	// always alphabetically sorted; empty strings and NULLs are dropped.
	// Cap output at MaxDistinctNetworks (hard cap to bound JSON size +
	// dropdown render perf).
	ListDistinctNetworks(
		ctx context.Context,
		instanceName domain.InstanceName,
	) ([]string, error)
}

// MaxDistinctNetworks bounds the distinct-network result so a
// degenerate dataset (thousands of unique network strings) can't
// blow up the facet-panel render or the JSON payload. A typical
// Sonarr instance has 5..30 distinct networks; the cap is set well
// above that with room for outliers.
const MaxDistinctNetworks = 256

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
	// SeriesCacheSortAirDateDesc orders by last_aired_at DESC (latest
	// aired episode first), with NULL last_aired_at sorted to the
	// bottom (upcoming series with no aired episodes yet). Tiebreak
	// on sonarr_series_id DESC for keyset stability.
	SeriesCacheSortAirDateDesc SeriesCacheSort = "air_date_desc"
)

// IsValid reports whether the sort key is one of the supported values.
func (s SeriesCacheSort) IsValid() bool {
	switch s {
	case SeriesCacheSortUpdatedDesc, SeriesCacheSortTitleAsc, SeriesCacheSortAirDateDesc:
		return true
	}
	return false
}

// SeriesCacheFilter aggregates the optional narrowing predicates for
// ListByFilter. Empty fields mean "no narrowing".
type SeriesCacheFilter struct {
	// State narrows by derived membership (imported / missing / all).
	State SeriesCacheState
	// Search is a case-insensitive substring matched against `title`
	// OR `title_slug`. Story 120: the /series page can't reach
	// off-page rows with client-side filtering on top of cursor
	// pagination, so the search predicate moves to the repo edge.
	// Empty string ⇒ no filter. SQL wildcard chars (`%`, `_`, `\`)
	// in the input are escaped before substring match.
	Search string
	// MonitoredOnly is a tri-state narrowing predicate (story 121a).
	// nil ⇒ no filter (any value). non-nil pointer to true ⇒
	// monitored=true rows only. non-nil pointer to false ⇒
	// monitored=false rows only. The pointer encoding distinguishes
	// "operator did not toggle" from "operator explicitly chose
	// unmonitored", which a `bool` cannot.
	MonitoredOnly *bool
	// Networks narrows to the union of named broadcast networks.
	// Empty slice / nil ⇒ no filter. Hardened against `IN ()` SQL —
	// the repo edge skips the WHERE clause when len == 0. Values are
	// matched verbatim (case-sensitive) against series_cache.network;
	// the network strings come from Sonarr and are stable.
	Networks []string
}

// LastGrabInfo holds the aggregated grab data for the list endpoint.
// Keyed on (instance_name, sonarr_series_id) at the call site. Empty
// value (zero time + empty string) means "no grab yet".
type LastGrabInfo struct {
	LastGrabAt          time.Time
	LastImportedEpisode string
}
