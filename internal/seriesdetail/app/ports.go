// Package seriesdetail composes the canonical series detail
// document read by the SPA's series page (PRD v4 §5.6 / §9 row 1,
// story 215 / G-1). All repository access goes through narrow
// ports declared here — the composer never depends on a concrete
// repository type. Live Sonarr access is a single narrow port
// (SonarrQueueLister) so unreachable-Sonarr is testable without a
// real client.
package seriesdetail

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesCachePort resolves (instance, sonarr_series_id) → canon
// series_id. The composer's first call — failure here is the 404
// path (no errgroup, no degraded).
type SeriesCachePort interface {
	Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (series.CacheEntry, error)
}

// SeriesPort fetches the canonical series row by series.id. The
// H-1 cast composer additionally uses GetByTMDBID to resolve a
// person_credits.tmdb_media_id back to a canon series.id (see
// cast.go probeInLibrary). Story 551 added ListByIDs — the recommendations
// branch (composer.go loadRecommendations + recommendations.go
// GetRecommendations) batches the M=10-20 stub-hydration probe into
// one query rather than M individual Get calls.
type SeriesPort interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error)
	ListByIDs(ctx context.Context, ids []domain.SeriesID) ([]series.Canon, error)
	// Story 556 (E-1 Z7) — batch sibling of GetByTMDBID. CastComposer
	// uses it to resolve every cast member's TV credits in one
	// round-trip rather than per-credit. Returns rows in tmdb_id-asc
	// order; missing tmdb_ids dropped silently.
	ListByTMDBIDs(ctx context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error)
}

// PersonCreditsPort is the narrow port for the H-1 in_library
// probe. Returns the cross-reference rows TMDB emits via
// `/person/{id}/tv_credits` + `/movie_credits` — the composer
// only needs the media ids to intersect against live
// series_cache rows. Composer-local PersonCreditRef projection
// keeps this port free of the repository's wide PersonCredit
// struct (the cmd-server adapter in cmd/server/main.go does the
// projection).
type PersonCreditsPort interface {
	ListByPerson(ctx context.Context, personID int64) ([]PersonCreditRef, error)
}

// PersonCreditRef is the projection the H-1 cast composer reads
// from person_credits. Kept composer-local so the port doesn't
// depend on the repository's full PersonCredit type.
type PersonCreditRef struct {
	MediaType   string // "tv" | "movie"
	TMDBMediaID int
}

// EpisodesCountPort is the narrow port for total_episode_count.
// Implemented by EpisodesRepository.CountBySeries — see
// infrastructure/database/repositories/episodes_repository.go.
type EpisodesCountPort interface {
	CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error)
}

// SeriesTextsPort fetches the localised title/overview/tagline row
// with the §5.6 language-fallback semantics.
type SeriesTextsPort interface {
	GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesText, error)
}

// SeasonsPort lists every season row for a series, ordered by
// season_number ascending.
type SeasonsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonSeason, error)
}

// EpisodesPort lists every episode row for a series. The composer
// groups them by season_number client-side rather than running N
// queries — N+1 hostility.
type EpisodesPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonEpisode, error)
}

// EpisodeStatesPort lists per-instance file states for every episode
// in a series.
type EpisodeStatesPort interface {
	ListBySeries(ctx context.Context, instanceName domain.InstanceName, seriesID domain.SeriesID) ([]series.EpisodeState, error)
}

// SeasonStatsPort lists the per-(instance, series, season) Sonarr
// statistics projection persisted by SyncSeriesFromSonarr (story 377).
// Empty result OR an error both degrade silently — mapSeasons falls
// back to walking d.Seasons[].Episodes[].State.HasFile.
type SeasonStatsPort interface {
	ListBySeries(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) ([]series.SeasonStat, error)
}

// EpisodeTextsPort fetches localised episode text by composite PK.
// Story 550 collapsed the per-episode N+1 in branch b of the composer
// (composer.go loadSeasonsAndEpisodes + GetCanonicalSeasons) into the
// batched ListByEpisodeIDsWithFallback. GetWithFallback is kept on the
// port because it remains the natural choice for single-episode call
// sites (the SeriesFreshener probe + tests).
//
// The batched method applies the §5.6 fallback chain's first two tiers
// (requested language → en-US) in one round-trip; the §5.6 third tier
// (first-available by language ASC) is intentionally NOT applied on the
// batch path — the composer treats a missing entry the same way it
// treats ErrNotFound from GetWithFallback today (EpisodeDetail.Text
// stays nil), so dropping the third tier preserves observable behaviour
// without requiring a sqlite-hostile LATERAL/window subquery.
type EpisodeTextsPort interface {
	GetWithFallback(ctx context.Context, episodeID domain.EpisodeID, language string) (series.EpisodeText, error)
	ListByEpisodeIDsWithFallback(ctx context.Context, episodeIDs []domain.EpisodeID, language string) (map[domain.EpisodeID]series.EpisodeText, error)
}

// SeriesPeoplePort lists series_people rows. The composer filters
// to kind=Cast + LIMIT 10 on credit_order, then joins via PeoplePort.
type SeriesPeoplePort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID, kind people.SeriesCreditKind) ([]people.SeriesCredit, error)
}

// PeoplePort fetches multiple people by id; the composer batches the
// top-10 person_id list from SeriesPeople into one ListByIDs call.
type PeoplePort interface {
	ListByIDs(ctx context.Context, ids []int64) ([]people.Person, error)
}

// GenresPort lists genre ids attached to a series + resolves each
// via the localised name. Story 552 (E-1 Z3) added
// ListByIDsWithFallback: the seriesdetail composer batches the per-id
// i18n fetch into one bounded-round-trip call rather than N
// per-id Get calls.
type GenresPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Genre, error)
	ListByIDsWithFallback(ctx context.Context, ids []int64, language string) ([]taxonomy.Genre, error)
}

// KeywordsPort — same shape as GenresPort for keywords. Story 552
// added ListByIDsWithFallback for the composer + overview +
// tmdb_fallback batch path.
type KeywordsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Keyword, error)
	ListByIDsWithFallback(ctx context.Context, ids []int64, language string) ([]taxonomy.Keyword, error)
}

// NetworksPort lists network ids for a series + batch-fetches the
// network rows. Networks have no i18n (brand names).
type NetworksPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.Network, error)
}

// CompaniesPort — same shape as NetworksPort for
// production_companies. Companies feed the External Links / overview
// aside indirectly; not all UI surfaces render them but the DTO
// already includes the network strip — companies are reserved for a
// future Hero details expand. The composer reads them now to keep the
// branch slot stable.
type CompaniesPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.ProductionCompany, error)
}

// VideosPort lists trailer-eligible videos for a series. The composer
// filters for the "best" trailer per PRD §5.6.
type VideosPort interface {
	ListBySeriesAndType(ctx context.Context, seriesID domain.SeriesID, videoType string) ([]enrichpersistence.Video, error)
}

// ContentRatingsPort lists per-country age ratings; composer picks
// one via locale fallback.
type ContentRatingsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]enrichpersistence.ContentRating, error)
}

// ExternalIDsPort lists external provider ids for an entity (here:
// the series). The composer projects imdb/tmdb/tvdb into the
// ExternalLinks DTO struct.
type ExternalIDsPort interface {
	ListByEntity(ctx context.Context, entityType enrichment.EntityType, entityID int64) ([]enrichpersistence.ExternalID, error)
}

// RecommendationsPort lists recommended series ids. The composer
// then batch-fetches the recommended canon rows via SeriesPort.Get
// AND probes SeriesCacheRepo to compute the in_library flag.
type RecommendationsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]domain.SeriesID, error)
}

// EnrichmentFreshnessPort exposes the per-(entity, source) freshness
// view the composer's computeDegraded reads. SyncedAtFor surfaces the
// canon row's enrichment_*_synced_at column (NULL = never enriched);
// ErrorsFor returns the live enrichment_errors rows for the entity
// across all sources (composer filters per-source). 464a defines the
// port; 464b wires composer.go to it.
type EnrichmentFreshnessPort interface {
	// SyncedAtFor returns the last-success timestamp for
	// (entity_type=series, entity_id, source). nil = never enriched.
	// Reads series.enrichment_*_synced_at (column path).
	SyncedAtFor(ctx context.Context, seriesID domain.SeriesID, source enrichment.Source) (*time.Time, error)
	// ErrorsFor returns every live enrichment_errors row for
	// (entity_type=series, entity_id) across all sources. Empty
	// slice when no rows match (NOT ports.ErrNotFound — "no errors"
	// is the happy path).
	ErrorsFor(ctx context.Context, seriesID domain.SeriesID) ([]enrichment.EnrichmentError, error)
}

// SeriesCacheLookupPort resolves a series.id → list of
// (instance_name, sonarr_series_id) for the recommendations
// in_library probe. This is the ONE new repository method this
// story adds (see series_cache_repository.go::ListBySeriesID).
type SeriesCacheLookupPort interface {
	ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error)
	// Story 556 (E-1 Z7) — batch sibling. CastComposer uses it to probe
	// in_library for every resolved (person → tv credit → canon series)
	// in one round-trip. Returns a map keyed on series.id; missing ids
	// map to nil so callers can probe O(1).
	ListBySeriesIDs(ctx context.Context, seriesIDs []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error)
}

// SonarrQueueLister is the single live port — the local Sonarr
// /queue endpoint (story 210 added it). Test fakes return canned
// payloads or errors; the unreachable case surfaces as a
// degraded[] entry, NOT a 5xx.
type SonarrQueueLister interface {
	Queue(ctx context.Context, seriesID domain.SonarrSeriesID) (sonarr.QueuePayload, error)
}

// MediaHashLookupPort moved to internal/shared/media.HashLookupPort in
// story 526 (shared MediaResolver extraction). The resolver type lives
// in that package now; this file no longer re-declares the port.

// OnDemandEnricher triggers a fire-and-forget enrichment enqueue for a
// canonical series. Used by TMDBFallbackUseCase to lazily lift stub-row
// detail pages on first user view (Story 528 / Bug 1 from backlog).
//
// Contract:
//   - EnqueueIfStale MUST return immediately (no blocking I/O, no
//     network calls). Implementations goroutine the actual dispatcher
//     call internally.
//   - hydration == series.HydrationFull → no-op (already enriched).
//   - hydration != full → enqueue at PriorityHot (user-visible request
//     is waiting on it).
//   - Implementations SHOULD throttle duplicate enqueues per seriesID
//     to avoid flooding the dispatcher when the SPA polls /series/{id}
//     during the 5-30s enrichment window.
//   - nil-OK seam: TMDBFallbackUseCase guards with `if uc.enricher != nil`,
//     so the use case still functions when enrichment is disabled at
//     boot (matches PersonEnqueuerHolder convention).
type OnDemandEnricher interface {
	EnqueueIfStale(seriesID domain.SeriesID, hydration series.Hydration)
}

// SeriesFreshener guarantees a series row is fresh in DB before the
// composer reads it. A5 (Story 563) added EnsureFreshScope as the
// primary orchestration method; EnsureFresh stays as a legacy shim
// (delegates to EnsureFreshScope with a canned Section list).
//
// Implementations MUST:
//   - singleflight per (seriesID, section, lang) to coalesce concurrent
//     first-time opens of the same series+section.
//   - hard ≤SyncTimeout (default 3s) for Mode==Sync; longer detached
//     budget for Mode==Async (~180s).
//   - on timeout/error: enqueue async refresh (Story 528 path),
//     return FreshenResult{Degraded: true} WITHOUT blocking past
//     SyncTimeout.
//   - on success: data is written to DB; caller may now re-read.
//
// EnsureFreshScope is idempotent and safe to call on EVERY detail handler
// entry. Nil-OK at every call site — when the field is nil, the
// composer just reads what's already in the DB.
type SeriesFreshener interface {
	// EnsureFreshScope — A5 driver (Story 563). Routes Probe verdicts to
	// narrow Worker methods (A2/A3a/A3b/A4). See EnsureFreshMode doc for
	// Sync/Async semantics + force propagation + seasonNumbers.
	EnsureFreshScope(
		ctx context.Context,
		seriesID domain.SeriesID,
		lang string,
		sections []freshener.Section,
		seasonNumbers []int,
		force bool,
		mode EnsureFreshMode,
	) (FreshenResult, error)

	// EnsureFresh — legacy shim (pre-A5). Delegates to EnsureFreshScope
	// with sections=[Skeleton, Overview, Cast, Recommendations, Media],
	// seasonNumbers=nil, force=false, mode=Sync. Kept for fakeFreshener
	// tests + existing tmdb_fallback_usecase callsites during incremental
	// migration. Post-Phase-2 removal.
	EnsureFresh(ctx context.Context, seriesID domain.SeriesID, lang string) FreshenResult
}

// FreshenResult tells the caller what happened. Used for the
// `degraded[]` projection on the response AND for the metric label.
type FreshenResult struct {
	// Refreshed: TMDB call ran AND the DB row was updated this call.
	Refreshed bool
	// Fresh: staleness check decided no refresh was needed.
	Fresh bool
	// Degraded: refresh timed out/errored; async fallback was enqueued.
	Degraded bool
}
