package dto

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// GapReportDTO — body of GET /api/v1/insights/gaps. Detects library
// gaps: monitored, already-aired, fileless canonical episodes (specials
// excluded), per Sonarr instance. Instances holds one element per
// instance (single element when ?instance= is given).
type GapReportDTO struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Instances   []GapInstanceDTO `json:"instances"`
}

// GapInstanceDTO — per-instance gap totals + bounded series drill-down.
// MissingEpisodeCount and WholeSeasonMissingCount are exact instance-wide
// totals; Series is the bounded (top-50) drill-down.
type GapInstanceDTO struct {
	InstanceName            string         `json:"instance_name" example:"main"`
	MissingEpisodeCount     int            `json:"missing_episode_count" example:"12"`
	WholeSeasonMissingCount int            `json:"whole_season_missing_count" example:"2"`
	Series                  []GapSeriesDTO `json:"series"`
}

// GapSeriesDTO — one series with ≥1 gap. MissingCount is the sum of its
// per-season exact missing counts present in the drill-down.
type GapSeriesDTO struct {
	SeriesID     domain.SeriesID `json:"series_id" example:"42"`
	Title        string          `json:"title" example:"The Expanse"`
	MissingCount int             `json:"missing_count" example:"5"`
	Seasons      []GapSeasonDTO  `json:"seasons"`
}

// GapSeasonDTO — per-season gap breakdown. WholeSeasonMissing is true
// when MissingCount == AiredMonitoredCount && AiredMonitoredCount > 0.
type GapSeasonDTO struct {
	SeasonNumber        int             `json:"season_number" example:"2"`
	MissingCount        int             `json:"missing_count" example:"3"`
	AiredMonitoredCount int             `json:"aired_monitored_count" example:"3"`
	WholeSeasonMissing  bool            `json:"whole_season_missing" example:"true"`
	Episodes            []GapEpisodeDTO `json:"episodes"`
}

// GapEpisodeDTO — one aired, monitored, fileless episode.
type GapEpisodeDTO struct {
	EpisodeID     domain.EpisodeID `json:"episode_id" example:"1001"`
	SeasonNumber  int              `json:"season_number" example:"2"`
	EpisodeNumber int              `json:"episode_number" example:"5"`
	AirDate       *time.Time       `json:"air_date"`
}
