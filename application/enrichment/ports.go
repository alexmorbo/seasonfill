// Package enrichment owns the workflow surface shared by the three
// enrichment workers (series, person, OMDb). Story 207 shipped the
// merge-policy / TTL / degraded helpers in domain/enrichment; story
// 209 adds the TMDBClient port here at the application layer. The
// port intentionally returns RAW infrastructure response types —
// the worker is the unit of merge-policy enforcement, and the
// mapper functions live next to the client. Workers import
// infrastructure/tmdb for both the constructor AND the mapper
// functions; the port abstraction exists for swap-out in tests,
// not for layer purity.
package enrichment

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/tmdb"
)

// TMDBClient is the substitution seam C-2 / C-3 use under test.
// The production implementation is *tmdb.Client (see
// infrastructure/tmdb). A test double implements this interface
// directly, returning fixture responses.
type TMDBClient interface {
	// GetTV fetches /tv/{id} with the canonical append_to_response.
	// Language is BCP-47 ("en-US" / "ru-RU").
	GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error)

	// GetSeason fetches /tv/{id}/season/{n}.
	GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error)

	// GetPerson fetches /person/{id} with tv_credits, movie_credits,
	// external_ids.
	GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error)

	// FindByTVDB resolves a tvdb_id to a TMDB id via /find. Returns
	// nil on empty result; the worker treats nil as
	// sync_log.outcome=not_found.
	FindByTVDB(ctx context.Context, tvdbID int64) (*tmdb.FindResponse, error)
}

// ----- new in 211 ----------------------------------------------------

// Priority controls dispatcher dequeue order. Hot beats Cold every
// time — the priority channel pair is drained hot-first in every
// worker loop. There are only two values; we do NOT generalise to
// numeric priorities (PRD §5.5 calls out "two priorities").
type Priority int

const (
	PriorityCold Priority = iota // background sweep + retries
	PriorityHot                  // user-initiated, just-added series
)

// EntityKind discriminates the dispatcher's queue. Series and Person
// have distinct workers; OMDb (D-1) will land its own EntityKind
// later. Keep the typed enum so a queue read with an unknown kind
// surfaces as a panic-free no-op (and a logged warning).
type EntityKind string

const (
	EntitySeries EntityKind = "series"
	EntityPerson EntityKind = "person"
	// 213 (D-1): OMDb enrichment runs against canonical series rows
	// with imdb_id present, but lives in its own dispatcher kind so
	// its goroutine + retry budget don't compete with the TMDB
	// series workers. The job's EntityID is a series.id (NOT a
	// series_cache.id).
	EntityOMDb EntityKind = "omdb"
)

// IsValid reports whether k is one of the known kinds.
func (k EntityKind) IsValid() bool {
	return k == EntitySeries || k == EntityPerson || k == EntityOMDb
}

// Job is one dispatch unit. EntityID maps to `series.id` for
// EntitySeries (canon row, NOT series_cache.id), `people.id` for
// EntityPerson. EnqueuedAt is stamped by the dispatcher — workers
// surface it on the slog line so latency is visible.
type Job struct {
	Kind       EntityKind
	EntityID   int64
	Priority   Priority
	EnqueuedAt time.Time
}

// Dispatcher is the substitution seam for tests + downstream
// callers that need to enqueue (sonarr_sync, webhook handler,
// nightly cron, refresh endpoint). Production impl is
// *enrichment.DispatcherImpl below; tests can pass a fake that
// records calls.
type Dispatcher interface {
	Enqueue(kind EntityKind, id int64, p Priority)
	Close()
}

// ----- series worker dependency ports --------------------------------
//
// The worker fans out across ~10 repositories. Each port is the
// narrow subset of one repository the worker actually calls —
// keeping every interface 1-to-2 methods so a fake is one struct.
// Production wiring binds these to the real repositories in
// cmd/server/enrichment_wiring.go.

// SeriesRepo: canon series + lookup-by-id (the dispatcher hands the
// worker an id; the worker pulls the current canon to feed
// MergeSeries).
type SeriesRepo interface {
	Get(ctx context.Context, id int64) (series.Canon, error)
	Upsert(ctx context.Context, c series.Canon) (int64, error)
	// UpsertStub — Story 319: see SeriesRepository.UpsertStub for
	// semantics. The recommendation loop in series_worker calls this
	// path so a stub upsert cannot blank a 'full' canon row's
	// poster_asset / backdrop_asset / hydration.
	UpsertStub(ctx context.Context, c series.Canon) (int64, error)
}

type SeriesTextsRepo interface {
	Upsert(ctx context.Context, t series.SeriesText) error
}

type SeasonsRepo interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonSeason, error)
	Upsert(ctx context.Context, s series.CanonSeason) (int64, error)
}

type EpisodesRepo interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonEpisode, error)
	BatchUpsert(ctx context.Context, eps []series.CanonEpisode) ([]int64, error)
}

type EpisodeTextsRepo interface {
	Upsert(ctx context.Context, t series.EpisodeText) error
}

type PeopleRepo interface {
	GetByTMDBID(ctx context.Context, tmdbID int) (people.Person, error)
	Upsert(ctx context.Context, p people.Person) (int64, error)
}

type SeriesPeopleRepo interface {
	BatchUpsert(ctx context.Context, credits []people.SeriesCredit) ([]int64, error)
}

// GenresRepo / KeywordsRepo / NetworksRepo / CompaniesRepo —
// "resolve or create + set join" surface, matching the repository
// methods shipped in B-3.
type GenresRepo interface {
	Upsert(ctx context.Context, g taxonomy.Genre) (int64, error)
	UpsertI18n(ctx context.Context, genreID int64, language, name string) error
	Set(ctx context.Context, seriesID int64, ids []int64) error
}

type KeywordsRepo interface {
	Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error)
	UpsertI18n(ctx context.Context, keywordID int64, language, name string) error
	Set(ctx context.Context, seriesID int64, ids []int64) error
}

type NetworksRepo interface {
	Upsert(ctx context.Context, n taxonomy.Network) (int64, error)
	Set(ctx context.Context, seriesID int64, ids []int64) error
}

type CompaniesRepo interface {
	Upsert(ctx context.Context, c taxonomy.ProductionCompany) (int64, error)
	Set(ctx context.Context, seriesID int64, ids []int64) error
}

type VideosRepoPort interface {
	// Upsert one row; the worker iterates the mapped slice. Uses
	// (series_id, tmdb_id) natural key on the repo side.
	Upsert(ctx context.Context, v VideoRow) error
}

type ContentRatingsRepoPort interface {
	Upsert(ctx context.Context, seriesID int64, country, rating string) error
}

type ExternalIDsRepoPort interface {
	Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error
}

type RecommendationsRepoPort interface {
	// Set replaces the join list. recommendedIDs are CANON series
	// ids (the worker upserts stubs first, then writes the join).
	Set(ctx context.Context, seriesID int64, recommendedIDs []int64) error
}

// SyncLogRepo is the journal write surface — Upsert is single-row;
// the dispatcher reads StaleScan + RetryDue on the cron path.
type SyncLogRepo interface {
	Upsert(ctx context.Context, e enrichment.SyncLog) error
	GetLastSync(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.SyncLog, error)
	StaleScan(ctx context.Context, source enrichment.Source, cutoff time.Time, limit int) ([]enrichment.SyncLog, error)
	RetryDue(ctx context.Context, source enrichment.Source, now time.Time, limit int) ([]enrichment.SyncLog, error)
}

// Transactor lifts the application/ports.Transactor surface into
// this package so the worker can wrap the multi-repo upsert in one
// tx without taking a dependency on application/ports. The
// production impl is *repositories.GormTransactor; the test impl
// is an inline closure-runner.
type Transactor interface {
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// MediaPrewarmer is the F-1 enqueue surface. Each request carries
// the canonical upstream URL (already fully-qualified with the size
// variant), the descriptive kind, and the file extension. The
// production impl is *application/media.Enqueuer; nil-OK seam stays
// for tests that don't care about pre-warm.
type MediaPrewarmer interface {
	Enqueue(ctx context.Context, reqs []MediaPrewarmRequest)
}

// MediaPrewarmRequest is the producer-side payload. Re-declared here
// (rather than importing application/media) so the dependency goes
// app/enrichment → app/media (downstream port), not the reverse.
// The two structs are mirrors; the wiring layer translates.
type MediaPrewarmRequest struct {
	UpstreamURL string
	Kind        string
	Extension   string
}

// VideoRow is the worker → VideosRepoPort transfer shape. Kept as
// a local struct (NOT tmdb.MappedVideo) so the application layer
// does NOT import infrastructure/tmdb just to talk to the videos
// repo. The worker translates the tmdb.MappedVideo slice into this
// shape inside applyTVPayload.
type VideoRow struct {
	SeriesID    int64
	TMDBID      string
	Language    string
	Country     string
	Name        string
	Key         string
	Site        string
	Type        string
	Official    bool
	PublishedAt *time.Time
	Size        int
}

// ----- new in 212 ----------------------------------------------------

// PeopleWritePort is the person_worker's write surface against
// people. Distinct from PeopleRepo (which is read-mostly for
// series_worker's stub-resolve path) because the person_worker needs
// the *language-aware* Get to read the existing hydration level and a
// PK-targeted Upsert that lifts the stub row to full. The production
// impl is *PeopleRepository (same repo, different port — Go
// duck-typing).
type PeopleWritePort interface {
	Get(ctx context.Context, id int64, language string) (people.Person, error)
	Upsert(ctx context.Context, p people.Person) (int64, error)
}

// PersonBiographiesPort persists localised biography rows. The
// fallback-read surface lives elsewhere (composer); the worker only
// writes.
type PersonBiographiesPort interface {
	Upsert(ctx context.Context, b people.PersonBiography) error
}

// PersonCreditsPort is the batch-upsert surface for person_credits.
// One INSERT … ON CONFLICT round-trip per chunk; the worker chunks at
// personCreditsBatchSize.
type PersonCreditsPort interface {
	BatchUpsert(ctx context.Context, credits []people.PersonCredit) ([]int64, error)
}

// ColdStartScanner is the application-layer port for the canonical
// series query "give me ids of series WITHOUT a sync_log row for the
// given source". Production impl is a method on SeriesRepository
// (Story 212 §8); tests pass a slice-backed fake.
type ColdStartScanner interface {
	ListMissingSyncLog(ctx context.Context, source string, limit int) ([]int64, error)
	// ListCanonImagesCorrupted — Story 319: returns series.id rows
	// where the canon is past stub phase but poster_asset or
	// backdrop_asset is NULL, so the boot one-shot recovery sweep can
	// enqueue them at PriorityCold for the TMDB re-sync to repopulate.
	ListCanonImagesCorrupted(ctx context.Context, limit int) ([]int64, error)
}
