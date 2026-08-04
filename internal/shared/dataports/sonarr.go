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
// MonitorMode is retained for backward-compatible request decode only —
// ADR-0011 S1 no longer maps it to addOptions.monitor; it is inert on
// the wire.
//
// Story 524 (N-4 per-season picker): when Seasons is non-empty the
// client serialises the explicit `seasons` array on the POST body and
// Sonarr honours per-season `monitored` flags directly. Per ADR-0011 S1
// the client no longer sends addOptions.monitor, so seasons[] per-season
// `monitored` is the sole per-season monitoring driver.
type AddSeriesPayload struct {
	TVDBID           int
	Title            string
	TitleSlug        string
	Year             int
	QualityProfileID int
	RootFolderPath   string
	Monitored        bool
	MonitorMode      string
	SearchOnAdd      bool
	Tags             []int
	Seasons          []SeasonSelection
	Images           []LookupImage
}

// SeasonSelection is one entry in AddSeriesPayload.Seasons — Sonarr's
// per-season monitored flag at create time.
type SeasonSelection struct {
	SeasonNumber int
	Monitored    bool
}

// SonarrLookupResult is the application-layer projection of one row in
// Sonarr's GET /api/v3/series/lookup response. Story 524 N-4 per-season
// picker — the FE preview surfaces Seasons (non-special, count > 0)
// so the operator can pick which seasons to monitor before the add.
//
// ImageURL is best-effort: Sonarr returns `remotePoster` for lookup
// results (the series is not yet added so there is no local MediaCover
// proxy). The FE consumes it as-is via the existing mediaUrl helper.
//
// ADR-0010 S1: TitleSlug and the full Images list are carried so the
// AddToSonarrUseCase can forward the complete series spec Sonarr
// requires on POST /api/v3/series (a lookup-less add is rejected with
// "Title must not be empty").
type SonarrLookupResult struct {
	Title     string
	TitleSlug string
	Year      int
	TVDBID    int
	TMDBID    int
	Overview  string
	ImageURL  string
	Images    []LookupImage
	Seasons   []SeasonInfo
}

// LookupImage is one entry in Sonarr's lookup images[] list, mirrored
// verbatim so the add flow can echo the exact coverType + URL Sonarr
// returned. RemoteURL is the fully-qualified source; URL may be a
// relative /MediaCover path for already-added series.
type LookupImage struct {
	CoverType string
	RemoteURL string
	URL       string
}

// SeasonInfo is one season entry in the lookup preview. EpisodeCount
// is 0 for seasons Sonarr has not yet enriched from TVDB (or for the
// special "Season 0" specials row); the FE collapses zero-episode
// rows by default.
type SeasonInfo struct {
	SeasonNumber int
	EpisodeCount int
	Monitored    bool
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
	// LookupSeries calls GET /api/v3/series/lookup?term={term} —
	// Sonarr's metadata preview that returns series shape (incl.
	// seasons[]) without requiring the series to be added yet. Story
	// 524 N-4 per-season picker. `term` is a free-form Sonarr query;
	// the discovery flow passes "tvdb:<id>" for a deterministic single-
	// row match. Returns the empty slice (no error) on Sonarr "no
	// matches"; the caller surfaces 404.
	LookupSeries(ctx context.Context, term string) ([]SonarrLookupResult, error)
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
	// SetSeasonMonitored flips the monitored flag of one season (GET full → flip → PUT whole body).
	SetSeasonMonitored(ctx context.Context, sonarrSeriesID domain.SonarrSeriesID, seasonNumber int, monitored bool) error
	// SearchSeason posts a SeasonSearch command for (seriesId, seasonNumber).
	SearchSeason(ctx context.Context, sonarrSeriesID domain.SonarrSeriesID, seasonNumber int) error
	// ParseRelease calls Sonarr /api/v3/parse for the given release
	// title. Tolerant of un-recognised titles — returns a zero-value
	// ParseResult and nil error. 4xx/5xx surface as the existing
	// StatusError shape via the client's `do` chain.
	ParseRelease(ctx context.Context, title string) (ParseResult, error)
	Name() string
}
