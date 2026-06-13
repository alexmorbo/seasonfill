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

	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

// SeriesCachePort resolves (instance, sonarr_series_id) → canon
// series_id. The composer's first call — failure here is the 404
// path (no errgroup, no degraded).
type SeriesCachePort interface {
	Get(ctx context.Context, instanceName string, sonarrSeriesID int) (series.CacheEntry, error)
}

// SeriesPort fetches the canonical series row by series.id.
type SeriesPort interface {
	Get(ctx context.Context, id int64) (series.Canon, error)
}

// SeriesTextsPort fetches the localised title/overview/tagline row
// with the §5.6 language-fallback semantics.
type SeriesTextsPort interface {
	GetWithFallback(ctx context.Context, seriesID int64, language string) (series.SeriesText, error)
}

// SeasonsPort lists every season row for a series, ordered by
// season_number ascending.
type SeasonsPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonSeason, error)
}

// EpisodesPort lists every episode row for a series. The composer
// groups them by season_number client-side rather than running N
// queries — N+1 hostility.
type EpisodesPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonEpisode, error)
}

// EpisodeStatesPort lists per-instance file states for every episode
// in a series.
type EpisodeStatesPort interface {
	ListBySeries(ctx context.Context, instanceName string, seriesID int64) ([]series.EpisodeState, error)
}

// EpisodeTextsPort fetches localised episode text by composite PK.
// The composer falls back per-row via the helper's two-LEFT-JOIN
// pattern (PRD §5.6), but the simpler interface used here is the
// per-episode GetWithFallback — N episodes × 1 call is still
// cheap on local sqlite (< 100 episodes for a typical series).
// E-1 may collapse this into one batched SQL later; for 215 the
// per-row call is intentional simplicity.
type EpisodeTextsPort interface {
	GetWithFallback(ctx context.Context, episodeID int64, language string) (series.EpisodeText, error)
}

// SeriesPeoplePort lists series_people rows. The composer filters
// to kind=Cast + LIMIT 10 on credit_order, then joins via PeoplePort.
type SeriesPeoplePort interface {
	ListBySeries(ctx context.Context, seriesID int64, kind people.SeriesCreditKind) ([]people.SeriesCredit, error)
}

// PeoplePort fetches multiple people by id; the composer batches the
// top-10 person_id list from SeriesPeople into one ListByIDs call.
type PeoplePort interface {
	ListByIDs(ctx context.Context, ids []int64) ([]people.Person, error)
}

// GenresPort lists genre ids attached to a series + resolves each
// via the localised name. Two methods because the composer needs
// the id list to issue the i18n fetch, and we don't want a JOIN
// repo method that locks both interfaces.
type GenresPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Genre, error)
}

// KeywordsPort — same shape as GenresPort for keywords.
type KeywordsPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Keyword, error)
}

// NetworksPort lists network ids for a series + batch-fetches the
// network rows. Networks have no i18n (brand names).
type NetworksPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]int64, error)
	ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.Network, error)
}

// CompaniesPort — same shape as NetworksPort for
// production_companies. Companies feed the External Links / overview
// aside indirectly; not all UI surfaces render them but the DTO
// already includes the network strip — companies are reserved for a
// future Hero details expand. The composer reads them now to keep the
// branch slot stable.
type CompaniesPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]int64, error)
	ListByIDs(ctx context.Context, ids []int64) ([]taxonomy.ProductionCompany, error)
}

// VideosPort lists trailer-eligible videos for a series. The composer
// filters for the "best" trailer per PRD §5.6.
type VideosPort interface {
	ListBySeriesAndType(ctx context.Context, seriesID int64, videoType string) ([]repositories.Video, error)
}

// ContentRatingsPort lists per-country age ratings; composer picks
// one via locale fallback.
type ContentRatingsPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]repositories.ContentRating, error)
}

// ExternalIDsPort lists external provider ids for an entity (here:
// the series). The composer projects imdb/tmdb/tvdb into the
// ExternalLinks DTO struct.
type ExternalIDsPort interface {
	ListByEntity(ctx context.Context, entityType enrichment.EntityType, entityID int64) ([]repositories.ExternalID, error)
}

// RecommendationsPort lists recommended series ids. The composer
// then batch-fetches the recommended canon rows via SeriesPort.Get
// AND probes SeriesCacheRepo to compute the in_library flag.
type RecommendationsPort interface {
	ListBySeries(ctx context.Context, seriesID int64) ([]int64, error)
}

// SyncLogPort exposes the per-(entity, source) sync_log lookup the
// degraded[] computation needs. The composer collects the four
// TMDB/OMDb sources for the series in one go.
type SyncLogPort interface {
	GetLastSync(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.SyncLog, error)
}

// SeriesCacheLookupPort resolves a series.id → list of
// (instance_name, sonarr_series_id) for the recommendations
// in_library probe. This is the ONE new repository method this
// story adds (see series_cache_repository.go::ListBySeriesID).
type SeriesCacheLookupPort interface {
	ListBySeriesID(ctx context.Context, seriesID int64) ([]series.CacheEntry, error)
}

// SonarrQueueLister is the single live port — the local Sonarr
// /queue endpoint (story 210 added it). Test fakes return canned
// payloads or errors; the unreachable case surfaces as a
// degraded[] entry, NOT a 5xx.
type SonarrQueueLister interface {
	Queue(ctx context.Context, seriesID int) (sonarr.QueuePayload, error)
}
