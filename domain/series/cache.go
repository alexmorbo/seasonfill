package series

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CacheEntry is the domain shape of one series_cache row (D66). It is
// the transport between the SeriesCacheRepository and the application
// layer (scan handler, queue handler, webhook handler in 041e/041f).
// All optional Sonarr fields are *T; Genres is the parsed slice
// (repository handles JSON serialisation in both directions). DeletedAt
// is *time.Time because rows are soft-deleted to preserve grab_records
// references.
//
// MissingCount (045a / B3) is the cached aired-missing episode count
// for the whole series, persisted at upsert from
// series.Statistics.AiredMissing(). Pre-migration rows default to 0;
// the list endpoint's state=missing filter treats 0 as "not missing".
type CacheEntry struct {
	InstanceName   string
	SonarrSeriesID int
	// SeriesID is the resolved canon series.id (set post-cutover
	// when the cache row's INNER JOIN to `series` succeeds). nil
	// only on a broken row (pre-cutover legacy data); the
	// composer treats nil as the 404 path. Read-only on the
	// domain shape; writes go through SeriesCacheRepository.Upsert
	// which resolves-or-creates the canon row.
	SeriesID  *domain.SeriesID
	Title     string
	TitleSlug string
	Year      *int
	TVDBID    *int
	IMDBID    *string
	TMDBID    *int
	Status    *string
	// Network REMOVED in E-1 (Story 210). Network membership lives in
	// series_networks join, read via SeriesCacheRepository.ListDistinctNetworks
	// or per-series resolved through NetworksRepository.ListBySeries.
	Genres         []string
	RuntimeMinutes *int
	Monitored      bool
	Overview       *string
	// PosterAsset is the raw canon poster path (e.g. "/abc.jpg") as
	// stored on `series.poster_asset`. Read straight from the canon
	// JOIN — no dependency on media_assets row existence. Handler-side
	// helpers derive the content-addressed media hash from this path
	// deterministically (sha256 over the synthetic CDN URL), so the
	// FE can request /media/<hash> the moment the canon row carries
	// a path, even before the byte download has completed. nil when
	// the canon row has no poster path → FE renders monogram.
	PosterAsset  *string
	FanartPath   *string
	BannerPath   *string
	MissingCount int
	// Story 374: cached Sonarr statistics. EpisodeFileCount and
	// SizeOnDiskBytes power LibraryStrip without depending on
	// episode_states rollups.
	EpisodeFileCount int
	SizeOnDiskBytes  int64
	// Story 376: AiredEpisodeCount is Sonarr's airedEpisodeCount —
	// the number of episodes whose air date is in the past. Used as
	// the denominator for LibraryStrip percentage so unaired future
	// episodes do not depress the headline.
	AiredEpisodeCount int
	// LastAiredAt mirrors Sonarr's `previousAiring` — the datetime of
	// the most recently aired episode for this series. Nil when no
	// episode has aired yet (upcoming series). Powers the F11
	// `air_date_desc` sort.
	LastAiredAt *time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// IsActive reports whether the entry is non-soft-deleted. The
// repository's ListActiveByInstance only returns entries where
// IsActive() would return true, but Get() returns soft-deleted rows
// too (so the webhook SeriesAdd path can resurrect an undeleted
// version without losing companion fields).
func (e CacheEntry) IsActive() bool { return e.DeletedAt == nil }
