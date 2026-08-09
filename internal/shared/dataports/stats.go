package dataports

import "context"

// StatsTotals is the per-instance library rollup from series_cache
// (deleted_at IS NULL). SeriesCount is live cached series; EpisodesOnDisk
// is SUM(episode_file_count); TotalSizeBytes is SUM(size_on_disk_bytes).
type StatsTotals struct {
	SeriesCount    int
	EpisodesOnDisk int
	TotalSizeBytes int64
}

// StatsKindBucket is one top-N genre/network row: a resolved display Name
// plus the instance-scoped distinct-series count and summed size on disk.
// Ordered by SizeBytes DESC. Name may be "" for a genre with no genres_i18n
// row (edge case) — the FE renders a placeholder.
type StatsKindBucket struct {
	Name        string
	SeriesCount int
	SizeBytes   int64
}

// StatsGrabCounts is the terminal-state breakdown of grab_records for the
// instance. Failed already folds grab_failed + import_failed. Grabbed is the
// transient in-flight bucket. The usecase derives success_rate from these.
type StatsGrabCounts struct {
	Grabbed  int
	Imported int
	Failed   int
}

// StatsTorrentTotals is the per-instance qbit_torrents rollup over present
// rows. AvgRatio is COALESCE(AVG(ratio),0) — the per-torrent mean.
type StatsTorrentTotals struct {
	TorrentCount         int
	TotalUploadedBytes   int64
	TotalDownloadedBytes int64
	AvgRatio             float64
}

// StatsRepository surfaces the read-only library-statistics aggregations
// backing GET /api/v1/insights/stats. Every method is a bounded
// COUNT/SUM/AVG or top-N GROUP BY over an EXISTING table; no writes, no
// migration. SQL is dialect-portable (no NOW()/INTERVAL/FILTER/cast;
// CASE/COALESCE/LIMIT only) so the SQLite test lane and the Postgres prod
// target agree. Every predicate is instance-scoped.
type StatsRepository interface {
	// DistinctInstances lists instance_name values with at least one live
	// series_cache row (deleted_at IS NULL), ordered ascending.
	DistinctInstances(ctx context.Context) ([]string, error)
	// Totals rolls up live series_cache rows for the instance.
	Totals(ctx context.Context, instance string) (StatsTotals, error)
	// ByGenre returns the top-`limit` genres by summed size_on_disk_bytes
	// (DESC, genre_id ASC tiebreak) over live series_cache joined to
	// series_genres, with a single localized name per genre.
	ByGenre(ctx context.Context, instance string, limit int) ([]StatsKindBucket, error)
	// ByNetwork returns the top-`limit` networks by summed size_on_disk_bytes
	// (DESC, network_id ASC tiebreak) over live series_cache joined to
	// series_networks + networks.
	ByNetwork(ctx context.Context, instance string, limit int) ([]StatsKindBucket, error)
	// GrabSuccess returns the terminal-state grab_records breakdown for the
	// instance (grabbed / imported / failed).
	GrabSuccess(ctx context.Context, instance string) (StatsGrabCounts, error)
	// TorrentTotals rolls up present=true qbit_torrents rows for the instance.
	TorrentTotals(ctx context.Context, instance string) (StatsTorrentTotals, error)
}
