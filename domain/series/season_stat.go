package series

import "time"

// SeasonStat is the per-(instance, series, season) projection of Sonarr's
// season.statistics block, persisted in the season_stats table. Distinct
// from the existing SeasonStats value object (season_stats.go) which is a
// pure derivation used by the scan prefilter and has no persistence
// shape. The persisted shape has the wider set of counters mapSeasons
// needs (TotalEpisodeCount for episode_count display, AiredEpisodeCount
// for the missing-count clamp) plus monitored + size_on_disk_bytes for
// future header chrome.
//
// Story 377 introduces the persistence path; previous releases drew the
// same numbers by walking episode_states at the handler — that walk is
// empty for fully-on-disk seasons skipped by scan_skip_handled_seasons
// so the SeasonsAccordion header rendered 0/N. season_stats is written
// by SyncSeriesFromSonarr alongside series_cache and is read by the
// seriesdetail composer.
type SeasonStat struct {
	InstanceName      string
	SonarrSeriesID    int
	SeasonNumber      int
	EpisodeCount      int
	EpisodeFileCount  int
	TotalEpisodeCount int
	AiredEpisodeCount int
	Monitored         bool
	SizeOnDiskBytes   int64
	UpdatedAt         time.Time
	DeletedAt         *time.Time
}
