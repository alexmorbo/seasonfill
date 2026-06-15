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
	// SeriesID is the resolved canon series.id (set post-cutover
	// when the cache row's INNER JOIN to `series` succeeds). nil
	// only on a broken row (pre-cutover legacy data); the
	// composer treats nil as the 404 path. Read-only on the
	// domain shape; writes go through SeriesCacheRepository.Upsert
	// which resolves-or-creates the canon row.
	SeriesID  *int64
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
	// PosterHash is the content-addressed sha256 of the stored w342
	// hero poster, joined from media_assets when status='stored'. nil
	// when the row has not been warmed yet — the FE falls back to a
	// monogram placeholder via <MediaImage fallback="monogram">.
	// Composer-side resolver remains the recovery path. Story 348a.
	PosterHash   *string
	FanartPath   *string
	BannerPath   *string
	MissingCount int
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
