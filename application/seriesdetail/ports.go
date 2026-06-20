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

	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
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
// cast.go probeInLibrary).
type SeriesPort interface {
	Get(ctx context.Context, id domain.SeriesID) (series.Canon, error)
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error)
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
// The composer falls back per-row via the helper's two-LEFT-JOIN
// pattern (PRD §5.6), but the simpler interface used here is the
// per-episode GetWithFallback — N episodes × 1 call is still
// cheap on local sqlite (< 100 episodes for a typical series).
// E-1 may collapse this into one batched SQL later; for 215 the
// per-row call is intentional simplicity.
type EpisodeTextsPort interface {
	GetWithFallback(ctx context.Context, episodeID domain.EpisodeID, language string) (series.EpisodeText, error)
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
// via the localised name. Two methods because the composer needs
// the id list to issue the i18n fetch, and we don't want a JOIN
// repo method that locks both interfaces.
type GenresPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Genre, error)
}

// KeywordsPort — same shape as GenresPort for keywords.
type KeywordsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error)
	Get(ctx context.Context, id int64, language string) (taxonomy.Keyword, error)
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
	ListBySeriesAndType(ctx context.Context, seriesID domain.SeriesID, videoType string) ([]repositories.Video, error)
}

// ContentRatingsPort lists per-country age ratings; composer picks
// one via locale fallback.
type ContentRatingsPort interface {
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]repositories.ContentRating, error)
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
	ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]domain.SeriesID, error)
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
	ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error)
}

// SonarrQueueLister is the single live port — the local Sonarr
// /queue endpoint (story 210 added it). Test fakes return canned
// payloads or errors; the unreachable case surfaces as a
// degraded[] entry, NOT a 5xx.
type SonarrQueueLister interface {
	Queue(ctx context.Context, seriesID domain.SonarrSeriesID) (sonarr.QueuePayload, error)
}

// MediaHashLookupPort resolves (raw TMDB path + size) → sha256 hash of the
// media_assets row for the stored variant. Used by the composer to translate
// canon.PosterAsset (a TMDB raw path) into the wire field the frontend hands
// to /api/v1/media/:hash. Misses are ports.ErrNotFound — the composer leaves
// the wire field nil so the frontend renders a monogram placeholder for
// below-the-fold tiles; above-the-fold hero fields use the eager-hash path
// (story 320 — composer mints the hash + EnsurePending so the handler's
// pending-row sync fetch can find the source URL).
//
// The composer never builds the URL itself; it asks the resolver with the raw
// path + size and lets the resolver own the URL shape (kept consistent with
// application/media.BuildTMDBImageURL on the pre-warm producer side).
type MediaHashLookupPort interface {
	HashForSourceURL(ctx context.Context, sourceURL string) (string, error)
	// EnsurePending writes a media_assets row keyed by hash with
	// status='pending', source_url=sourceURL, kind=kind, created_at=now —
	// IFF the row doesn't already exist. Idempotent: a second call with
	// the same hash is a no-op (existing status='stored' / 'failed' is
	// preserved). The composer calls this on hero poster/backdrop lookup
	// miss so the handler (story 321) has a source URL to fetch from
	// when the frontend later requests /api/v1/media/:hash.
	EnsurePending(ctx context.Context, hash, sourceURL, kind string) error
}
