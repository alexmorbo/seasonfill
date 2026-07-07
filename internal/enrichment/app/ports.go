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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// TMDBClient is the substitution seam C-2 / C-3 use under test.
// The production implementation is *tmdb.Client (see
// infrastructure/tmdb). A test double implements this interface
// directly, returning fixture responses.
type TMDBClient interface {
	// GetTV fetches /tv/{id} with the canonical append_to_response.
	// Language is BCP-47 ("en-US" / "ru-RU").
	GetTV(ctx context.Context, id int64, language string) (*tmdb.TVResponse, error)

	// GetTVAllLangs fetches /tv/{id} for the S-B all-langs path: base-lang
	// (en-US) root fields + the translations sub-resource for every supported
	// language + a union include_image_language, in ONE round-trip.
	// RefreshSeriesAllLangs consumes it to populate series_texts /
	// series_media_texts for all supported langs at once.
	GetTVAllLangs(ctx context.Context, id int64) (*tmdb.TVResponse, error)

	// GetSeason fetches /tv/{id}/season/{n}.
	GetSeason(ctx context.Context, tvID int64, seasonNumber int, language string) (*tmdb.SeasonResponse, error)

	// GetPerson fetches /person/{id} with tv_credits, movie_credits,
	// external_ids.
	GetPerson(ctx context.Context, id int64, language string) (*tmdb.PersonResponse, error)

	// FindByTVDB resolves a tvdb_id to a TMDB id via /find. Returns
	// nil on empty result; the worker treats nil as a terminal
	// not-found (records an enrichment_errors row with attempts=99).
	FindByTVDB(ctx context.Context, tvdbID domain.TVDBID) (*tmdb.FindResponse, error)
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
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
	Upsert(ctx context.Context, c series.Canon) (domain.SeriesID, error)
	// UpsertStub — Story 319: see SeriesRepository.UpsertStub for
	// semantics. The recommendation loop in series_worker calls this
	// path so a stub upsert cannot blank a 'full' canon row's
	// poster_asset / backdrop_asset / hydration.
	UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error)
	// MarkTMDBSynced / MarkOMDBSynced — D-3 (464b): post-success
	// freshness stamps. Workers call these AFTER the multi-repo upsert
	// tx commits, alongside EnrichmentErrors.ClearOnSuccess.
	MarkTMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	MarkOMDBSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// MarkTextSynced — A2: stamps series.enrichment_text_synced_at = now.
	// Called by Worker.RefreshSeriesText after the series_texts UPSERT
	// commits inside the same tx. Single-column UPDATE — concurrent
	// Sonarr-driven Series.Upsert COALESCEs the stamp so this write is
	// not silently overwritten (A1 carry-forward I-1).
	MarkTextSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// MarkCastSynced — A2: stamps series.enrichment_cast_synced_at = now.
	// Called by Worker.RefreshCast after the person_credits BatchUpsert
	// commits inside the same tx. Same COALESCE protection as
	// MarkTextSynced — see seriesUpsertAssignments().
	MarkCastSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// MarkRecsSynced — A3b: stamps series.enrichment_recs_synced_at = now.
	// Called by Worker.RefreshRecommendations after the
	// series_recommendations.Set + N×UPSERT series_texts side-effect
	// commits inside the same tx. Single-column UPDATE — concurrent
	// Sonarr-driven Series.Upsert COALESCEs the stamp so this write is
	// not silently overwritten (see seriesUpsertAssignments()).
	MarkRecsSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// MarkMediaSynced — A4: stamps series.enrichment_media_synced_at = now.
	// Called by Worker.RefreshMediaAssets after the series.Upsert +
	// per-season Seasons.Upsert loop commits inside the same tx. Single-
	// column UPDATE — concurrent Sonarr-driven Series.Upsert COALESCEs the
	// stamp so this write is not silently overwritten (see
	// seriesUpsertAssignments() line 818 — pre-reserved slot from A2 I-1
	// defensive addition).
	MarkMediaSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// MarkSkeletonSynced — W18-16: stamps series.skeleton_synced_at = now for one
	// row. Called by HandleForcedLang AFTER the staged skeleton canon tx commits.
	// Single-column UPDATE (+ updated_at); it does NOT touch the shared
	// enrichment_tmdb_synced_at (the on-view worker deliberately leaves that for
	// the dispatcher-driven full Handle). Absent from seriesUpsertAssignments()
	// so a concurrent Sonarr scan preserves it by omission.
	MarkSkeletonSynced(ctx context.Context, id domain.SeriesID, now time.Time) error
	// UpdateOMDbColumns — W18-6 (M-1): plain-assigns the four OMDb-owned
	// columns (imdb_rating, imdb_votes, omdb_rated, omdb_awards) onto the
	// canon row, writing NULL for any nil pointer so an OMDb "N/A" response
	// CLEARS a previously-stored rating. The OMDb worker is the SOLE owner of
	// these columns; every other writer goes through the COALESCE Upsert path
	// (seriesUpsertAssignments) and cannot clobber them. Called inside the
	// worker's success tx, keyed by series id.
	UpdateOMDbColumns(ctx context.Context, id domain.SeriesID, rating *float64, votes *int, rated *string, awards *string) error
}

type SeriesTextsRepo interface {
	Upsert(ctx context.Context, t series.SeriesText) error
}

// SeriesRecCanonWriter — Story 571 B-54: narrow port used by A3b
// RefreshRecommendations to overwrite rec children's canon
// poster_asset + backdrop_asset with TMDB's lang-preferred paths
// returned in Recommendations.Results[*].{PosterPath,BackdropPath}.
//
// Separate from SeriesRepo.Upsert (Sonarr-authoritative full-column
// merge) and SeriesRepo.UpsertStub (COALESCE-preserve, which is the
// exact bug — existing en-US poster wins over new ru-RU path). This
// narrow writer overwrites ONLY the two media columns; other Sonarr
// authoritative columns stay untouched.
//
// Nil-OK — SeriesWorkerDeps.RecCanonWriter=nil degrades A3b to the
// pre-571 behavior (rec children keep whatever series_media_texts
// en-US row was first written). Preserves backwards compat for test
// fixtures that don't wire the writer.
type SeriesRecCanonWriter interface {
	// UpdateRecCanonMedia writes series_media_texts{en-US} poster/backdrop
	// for recSeriesID from the TMDB rec-summary paths. A non-empty path
	// overwrites the stored value (TMDB is authoritative for media per
	// PRD §5.4); an empty path preserves the prior value.
	//
	// No-op when both posterPath and backdropPath are empty.
	//
	// series_media_texts has an FK to series(id); A3b calls this after
	// UpsertStub creates the rec-child row in the same tx. DB IO errors
	// bubble as fmt.Errorf-wrapped.
	UpdateRecCanonMedia(ctx context.Context, recSeriesID domain.SeriesID, posterPath, backdropPath string) error
}

type SeasonsRepo interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonSeason, error)
	Upsert(ctx context.Context, s series.CanonSeason) (int64, error)
	// MarkSeasonEpisodesSynced — A3a: stamps seasons.episodes_synced_at = now
	// для (series_id, season_number). Called by Worker.RefreshSeasonSlim
	// after the episodes BatchUpsert + episode_texts.Upsert commits inside
	// the same tx. Composite-key single-column UPDATE — concurrent
	// Sonarr-driven Seasons.Upsert COALESCEs the stamp so this write is
	// not silently overwritten (A1 carry-forward I-2).
	MarkSeasonEpisodesSynced(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, now time.Time) error
}

type EpisodesRepo interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonEpisode, error)
	BatchUpsert(ctx context.Context, eps []series.CanonEpisode) ([]int64, error)
}

type EpisodeTextsRepo interface {
	Upsert(ctx context.Context, t series.EpisodeText) error
}

// SeasonTextsRepo persists one localised text row per
// (series_id, season_number, language) into season_texts. B3b: the
// narrow worker RefreshSeasonSlim writes the localised season name +
// overview from the same GetSeason payload it already fetched. The
// COALESCE-preserve on name/overview/enriched_at lives in the repo
// (SeasonTextsRepository.Upsert), so a partial (name-only) write never
// blanks a previously-stored overview. Nil-OK dep on SeriesWorkerDeps —
// see the field doc there.
type SeasonTextsRepo interface {
	Upsert(ctx context.Context, t series.SeasonText) error
}

// SeriesMediaTextsRepo persists one per-language poster/backdrop row
// (series_media_texts.Upsert). Nil-OK on SeriesWorkerDeps — when nil the
// RefreshSeriesText path skips the per-lang media write entirely and the
// read paths fall back to canon series.poster_asset (Story 584a). The
// production impl is enrichpersistence.SeriesMediaTextsRepository, whose
// COALESCE-preserve Upsert guards every column so a partial write never
// blanks a previously-fetched value.
type SeriesMediaTextsRepo interface {
	Upsert(ctx context.Context, t series.SeriesMediaText) error
}

// SeasonMediaTextsRepo persists one per-language season poster/backdrop row
// (season_media_texts.Upsert). Nil-OK on SeriesWorkerDeps — when nil the worker
// skips the season-media step. COALESCE-preserve Upsert guards every column so a
// partial write never blanks a prior value. S-C2.
type SeasonMediaTextsRepo interface {
	Upsert(ctx context.Context, t series.SeasonMediaText) error
}

type PeopleRepo interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (people.Person, error)
	Upsert(ctx context.Context, p people.Person) (int64, error)
}

// GenresRepo / KeywordsRepo / NetworksRepo / CompaniesRepo —
// "resolve or create + set join" surface, matching the repository
// methods shipped in B-3.
type GenresRepo interface {
	Upsert(ctx context.Context, g taxonomy.Genre) (int64, error)
	UpsertI18n(ctx context.Context, genreID int64, language, name string) error
	Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error
}

type KeywordsRepo interface {
	Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error)
	UpsertI18n(ctx context.Context, keywordID int64, language, name string) error
	Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error
}

type NetworksRepo interface {
	Upsert(ctx context.Context, n taxonomy.Network) (int64, error)
	Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error
}

type CompaniesRepo interface {
	Upsert(ctx context.Context, c taxonomy.ProductionCompany) (int64, error)
	Set(ctx context.Context, seriesID domain.SeriesID, ids []int64) error
}

type VideosRepoPort interface {
	// Upsert one row; the worker iterates the mapped slice. Uses
	// (series_id, tmdb_id) natural key on the repo side.
	Upsert(ctx context.Context, v VideoRow) error
}

type ContentRatingsRepoPort interface {
	Upsert(ctx context.Context, seriesID domain.SeriesID, country, rating string) error
}

type ExternalIDsRepoPort interface {
	Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error
}

type RecommendationsRepoPort interface {
	// Set replaces the join list. recommendedIDs are CANON series
	// ids (the worker upserts stubs first, then writes the join).
	Set(ctx context.Context, seriesID domain.SeriesID, recommendedIDs []domain.SeriesID) error
}

// EnrichmentErrorRepo is the D-3 failure write surface. The error-tracking
// half of what used to be the sync_log journal; the success half moves
// to direct canon-column writes via Series.MarkTMDBSynced / MarkOMDBSynced
// (people side: People.MarkSynced).
//
// Lifecycle per (entity_type, entity_id, source):
//   - First failure → RecordFailure inserts.
//   - Subsequent failures → RecordFailure upserts (bumps attempts +
//     last_seen_at + next_attempt_at; first_seen_at preserved).
//   - Success → ClearOnSuccess removes the row.
//   - Retry dispatcher → ListDueForRetry reads next_attempt_at <= now.
//
// 464a defines the port + ships the production EnrichmentErrorsRepository
// implementation; 464b wires it into the workers + composer.
type EnrichmentErrorRepo interface {
	RecordFailure(ctx context.Context, e enrichment.EnrichmentError) error
	ClearOnSuccess(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) error
	GetForEntity(ctx context.Context, entityType enrichment.EntityType, entityID int64) ([]enrichment.EnrichmentError, error)
	ListDueForRetry(ctx context.Context, source enrichment.Source, now time.Time, limit int) ([]enrichment.EnrichmentError, error)
	GetByEntitySource(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.EnrichmentError, error)
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

// MediaResolver narrows shared/media.Resolver to the ONE method
// RefreshMediaAssets (A4) calls. Kept as an interface so tests can pass
// a stub; the wiring layer hands the concrete *media.Resolver — the same
// instance the seriesdetail composer + discovery handler share.
//
// Under Story 347 unified-resolve contract (production default), Resolve
// mints eager sha256 hash + writes media_assets pending row inline for
// every non-nil rawPath, returning the hash pointer. The hash is NOT
// persisted onto series/seasons rows by A4 (those tables carry raw TMDB
// paths — poster_asset column is the raw path). A4 calls Resolve for the
// SIDE-EFFECT: minting the media_assets pending row so the composer's
// next cold Resolve() has a stable sha256 handle immediately.
//
// nil MediaResolver → A4 degrades gracefully (skips the eager-hash step,
// still writes raw paths + stamp). Parallels MediaPrewarmer nil-OK
// pattern in SeriesWorkerDeps.
type MediaResolver interface {
	Resolve(ctx context.Context, rawPath *string, size, kind string) *string
}

// VideoRow is the worker → VideosRepoPort transfer shape. Kept as
// a local struct (NOT tmdb.MappedVideo) so the application layer
// does NOT import infrastructure/tmdb just to talk to the videos
// repo. The worker translates the tmdb.MappedVideo slice into this
// shape inside applyTVPayload.
type VideoRow struct {
	SeriesID    domain.SeriesID
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
	// MarkSynced — D-3 (464b): stamps people.enrichment_synced_at on
	// successful TMDB person hydration. Called by PersonWorker after
	// the multi-repo upsert tx commits.
	MarkSynced(ctx context.Context, id int64, now time.Time) error
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

// PersonCreditsTextsPort is the per-language cast-character-name write
// surface (person_credits_texts, S-G). Separate from PersonCreditsPort so
// only the SeriesWorker (RefreshCast) depends on it — PersonWorker does not
// write localized character names. nil-OK on SeriesWorker.Deps.
type PersonCreditsTextsPort interface {
	BatchUpsert(ctx context.Context, texts []people.PersonCreditText) error
}

// PeopleTextsPort is the per-language person DISPLAY-name write surface
// (people_texts, Story 1083). Separate from PeopleRepo so only the
// SeriesWorker (RefreshCast) depends on it. nil-OK on SeriesWorker.Deps.
type PeopleTextsPort interface {
	BatchUpsert(ctx context.Context, texts []people.PersonText) error
}

// ColdStartScanner is the application-layer port for the canonical
// series-id queries the boot-time backfill + recovery loops consume.
// Production impl is *SeriesRepository (via wiring adapter); tests pass
// a slice-backed fake.
type ColdStartScanner interface {
	// ListMissingTMDBSync — D-3 column-on-canon query for the
	// cold-start backfill loop. Returns series.id rows whose
	// enrichment_tmdb_synced_at IS NULL (never enriched).
	ListMissingTMDBSync(ctx context.Context, limit int) ([]domain.SeriesID, error)
}
