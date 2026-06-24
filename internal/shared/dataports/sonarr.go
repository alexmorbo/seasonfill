package dataports

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ParseResult is the application-layer projection of the Sonarr
// /api/v3/parse response. Mirrors infrastructure/sonarr.ParseResult
// shape-for-shape; the adapter converts. Keeping the type here lets
// application code consume the result without an inbound dependency
// on infrastructure.
type ParseResult struct {
	Quality      string
	Source       string
	Resolution   int
	Languages    []string
	ReleaseGroup string
}

type QualityItem struct {
	ID     int
	Name   string
	Order  int
	Weight int
}

type QualityProfile struct {
	ID    int
	Name  string
	Items []QualityItem
}

type Indexer struct {
	ID       int
	Name     string
	Priority int
}

type HistoryEvent struct {
	EpisodeNumber int
	SeasonNumber  int
	GUID          string
	IndexerName   string
	IndexerID     int
	OccurredAtUTC string
}

type SystemStatus struct {
	Version     string
	InstanceURL string
}

type Tag struct {
	ID    int
	Label string
}

// AddSeriesPayload mirrors POST /api/v3/series. N-4c (story 520) input
// for AddToSonarrUseCase. TVDBID is the integer Sonarr lookup key;
// callers convert from the typed shareddomain.TVDBID at the call site.
// MonitorMode maps to Sonarr's addOptions.monitor — "all", "future",
// "missing", "none" (empty defaults to "all" at the client).
type AddSeriesPayload struct {
	TVDBID           int
	QualityProfileID int
	RootFolderPath   string
	Monitored        bool
	MonitorMode      string
	SearchOnAdd      bool
	Tags             []int
}

// AddSeriesResult is the post-create projection — only the Sonarr
// series id is consumed by the use case.
type AddSeriesResult struct {
	SonarrSeriesID int
}

// RootFolder is Sonarr's /api/v3/rootfolder row. N-4a foundation for
// the AddToSonarrModal "root folder" picker; consumed by the discovery
// /api/v1/instances/{name}/root-folders endpoint (N-4b).
//
// `Accessible` and `FreeSpace` are emitted by Sonarr but absent on
// older instances — both default to zero values when missing. The
// caller decides whether to filter inaccessible roots.
type RootFolder struct {
	ID         int
	Path       string
	Accessible bool
	FreeSpace  int64
}

// EpisodeFileDetail mirrors Sonarr's WebhookEpisodeFile + the on-disk
// metadata available from GET /api/v3/episodeFile. 043c: powers the
// Phase 12 drawer "Импортированные файлы" section. seasonfill does NOT
// persist this — it is fetched lazily per drawer open.
type EpisodeFileDetail struct {
	ID             int    // Sonarr's episodeFile.id
	RelativePath   string // path under the series root, e.g. "Season 02/Severance.S02E01.mkv"
	SeasonNumber   int
	EpisodeNumbers []int // mappedEpisodeNumbers; usually 1 entry, sometimes 2 for multi-ep files
	SizeBytes      int64
	Quality        string // Sonarr's quality.quality.name (e.g. "WEBDL-2160p")
}

//go:generate moq -out sonarr_mock.go . SonarrClient

type SonarrClient interface {
	SystemStatus(ctx context.Context) (SystemStatus, error)
	ListSeries(ctx context.Context) ([]series.Series, error)
	// ListSeriesCache fetches the same /api/v3/series payload as
	// ListSeries but maps to the richer series.CacheEntry shape used by
	// the series_cache repository (041e). instanceName is stamped onto
	// every returned entry — Sonarr does not echo it.
	ListSeriesCache(ctx context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error)
	GetSeries(ctx context.Context, id domain.SonarrSeriesID) (series.Series, error)
	ListEpisodes(ctx context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]series.Episode, error)
	// ListEpisodesBySeries returns every episode for a series in a
	// single Sonarr round-trip (GET /api/v3/episode?seriesId=). Used by
	// the queue Missing handler to embed per-episode presence inline
	// without N×ListEpisodes fan-out per request — the caller filters
	// to the seasons it wants in-memory. Episodes are returned in
	// Sonarr's natural order; callers that need a specific ordering
	// must sort.
	ListEpisodesBySeries(ctx context.Context, seriesID domain.SonarrSeriesID) ([]series.Episode, error)
	ListEpisodeFiles(ctx context.Context, seriesID domain.SonarrSeriesID) (map[int]int, error)
	// ListEpisodeFilesBySeason returns the rich per-file metadata from
	// /api/v3/episodeFile?seriesId=&seasonNumber=, filtered to the
	// requested season. Used by the 043c grab episode-files endpoint
	// (drawer "Импортированные файлы"). Capped at 200 entries
	// server-side; Sonarr's natural response is ≤ 1000 per season.
	ListEpisodeFilesBySeason(ctx context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]EpisodeFileDetail, error)
	SearchReleases(ctx context.Context, seriesID domain.SonarrSeriesID, seasonNumber int) ([]release.Release, error)
	GetQualityProfile(ctx context.Context, id int) (QualityProfile, error)
	// ListQualityProfiles fetches all quality profiles defined on the
	// Sonarr instance. Used by the N-4 AddToSonarrModal "quality profile"
	// picker. The returned QualityProfile.Items slice is left empty —
	// callers that need per-item allowance must fall back to
	// GetQualityProfile(id) for one-off lookups. List endpoint trades
	// per-item detail for a single round-trip.
	ListQualityProfiles(ctx context.Context) ([]QualityProfile, error)
	// ListRootFolders fetches Sonarr's configured root folders. Used by
	// the N-4 AddToSonarrModal "root folder" picker (N-4b cache).
	ListRootFolders(ctx context.Context) ([]RootFolder, error)
	ListIndexers(ctx context.Context) ([]Indexer, error)
	ListTags(ctx context.Context) ([]Tag, error)
	// CreateTag posts a new label to /api/v3/tag. Sonarr deduplicates by
	// label and returns the existing row on re-create, so callers can
	// invoke without a prior ListTags membership check — POST itself is
	// idempotent at the Sonarr layer. N-4c TagResolver uses this on
	// cache miss after ListTags returns no match.
	CreateTag(ctx context.Context, label string) (Tag, error)
	// AddSeries posts to /api/v3/series and returns the created series
	// id. N-4c discovery AddToSonarrUseCase consumer. Sonarr stores +
	// indexes asynchronously; the returned id is committed before this
	// call returns, but the per-season episode rows may take a few
	// seconds to materialise on the Sonarr side.
	AddSeries(ctx context.Context, payload AddSeriesPayload) (AddSeriesResult, error)
	GrabHistory(ctx context.Context, seriesID domain.SonarrSeriesID) ([]HistoryEvent, error)
	ForceGrab(ctx context.Context, guid string, indexerID int) (string, error)
	// ParseRelease calls Sonarr /api/v3/parse for the given release
	// title. Tolerant of un-recognised titles — returns a zero-value
	// ParseResult and nil error. 4xx/5xx surface as the existing
	// StatusError shape via the client's `do` chain.
	ParseRelease(ctx context.Context, title string) (ParseResult, error)
	Name() string
}
