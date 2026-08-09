package dto

import "time"

// StatsReportDTO — body of GET /api/v1/insights/stats. A read-only library
// statistics rollup per Sonarr instance (single element when ?instance= is
// given). Every aggregation is instance-scoped over existing tables.
type StatsReportDTO struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Instances   []StatsInstanceDTO `json:"instances"`
}

// StatsInstanceDTO — one instance's statistics block.
type StatsInstanceDTO struct {
	InstanceName  string                `json:"instance_name" example:"main"`
	Totals        StatsTotalsDTO        `json:"totals"`
	ByGenre       []StatsKindDTO        `json:"by_genre"`
	ByNetwork     []StatsKindDTO        `json:"by_network"`
	GrabSuccess   StatsGrabSuccessDTO   `json:"grab_success"`
	TorrentTotals StatsTorrentTotalsDTO `json:"torrent_totals"`
}

// StatsTotalsDTO — library rollup from series_cache (deleted_at IS NULL).
type StatsTotalsDTO struct {
	SeriesCount    int   `json:"series_count" example:"342"`
	EpisodesOnDisk int   `json:"episodes_on_disk" example:"9871"`
	TotalSizeBytes int64 `json:"total_size_bytes" example:"41231234567"`
}

// StatsKindDTO — one top-N genre or network bucket. The label field is
// `genre` for by_genre and `network` for by_network; the rest is shared.
type StatsKindDTO struct {
	Genre       string `json:"genre,omitempty" example:"Drama"`
	Network     string `json:"network,omitempty" example:"HBO"`
	SeriesCount int    `json:"series_count" example:"120"`
	SizeBytes   int64  `json:"size_bytes" example:"22345678901"`
}

// StatsGrabSuccessDTO — grab_records terminal-state breakdown. SuccessRate
// = imported / (imported + failed), 0..1, 0 when the denominator is 0.
type StatsGrabSuccessDTO struct {
	Grabbed     int     `json:"grabbed" example:"3"`
	Imported    int     `json:"imported" example:"512"`
	Failed      int     `json:"failed" example:"21"`
	SuccessRate float64 `json:"success_rate" example:"0.9606"`
}

// StatsTorrentTotalsDTO — qbit_torrents rollup over present rows. AvgRatio
// is the per-torrent mean.
type StatsTorrentTotalsDTO struct {
	TorrentCount         int     `json:"torrent_count" example:"88"`
	TotalUploadedBytes   int64   `json:"total_uploaded_bytes" example:"9988776655"`
	TotalDownloadedBytes int64   `json:"total_downloaded_bytes" example:"4433221100"`
	AvgRatio             float64 `json:"avg_ratio" example:"2.14"`
}
