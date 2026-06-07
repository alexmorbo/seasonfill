package series

import "time"

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
	Title          string
	TitleSlug      string
	Year           *int
	TVDBID         *int
	IMDBID         *string
	TMDBID         *int
	Status         *string
	Network        *string
	Genres         []string
	RuntimeMinutes *int
	Monitored      bool
	Overview       *string
	PosterPath     *string
	FanartPath     *string
	BannerPath     *string
	MissingCount   int
	UpdatedAt      time.Time
	DeletedAt      *time.Time
}

// IsActive reports whether the entry is non-soft-deleted. The
// repository's ListActiveByInstance only returns entries where
// IsActive() would return true, but Get() returns soft-deleted rows
// too (so the webhook SeriesAdd path can resurrect an undeleted
// version without losing the historical poster_path).
func (e CacheEntry) IsActive() bool { return e.DeletedAt == nil }
