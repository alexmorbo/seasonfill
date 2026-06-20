package series

import (
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type SeriesType string

const (
	SeriesTypeStandard SeriesType = "standard"
	SeriesTypeAnime    SeriesType = "anime"
	SeriesTypeDaily    SeriesType = "daily"
)

type Series struct {
	ID             domain.SonarrSeriesID
	Title          string
	Type           SeriesType
	TagIDs         []int
	Monitored      bool
	QualityProfile int
	Seasons        []Season
	Statistics     Statistics
}

func (s Series) MonitoredSeasons() []Season {
	out := make([]Season, 0, len(s.Seasons))
	for _, season := range s.Seasons {
		if season.Monitored {
			out = append(out, season)
		}
	}
	return out
}

// Statistics mirrors Sonarr's `series.statistics`. Zero values =
// "no statistics" (Sonarr omits the field for empty series).
//
// 046a extends the struct with Total / Aired so the evaluator can
// surface "total episodes ever" and "aired-so-far" alongside the
// already-tracked file count. Pre-046a callers that decoded an empty
// `statistics` block still get zero values; their downstream consumers
// already nil-tolerate that path (see AiredMissing's clamp).
type Statistics struct {
	EpisodeCount     int // legacy: pre-046a callers wrote into this
	EpisodeFileCount int
	Total            int // NEW: Sonarr totalEpisodeCount
	Aired            int // NEW: Sonarr airedEpisodeCount
	// Story 374: bytes Sonarr tracks for this series; authoritative for
	// the LibraryStrip hero tile (sum of episode-file sizes).
	SizeOnDisk int64
}

// AiredMissing returns aired-but-not-on-disk count, clamped to 0
// (Sonarr can return inconsistent snapshots mid-import).
//
// Prefers the NEW Aired field when set (>0). Falls back to the legacy
// EpisodeCount path (pre-046a behaviour) so older callers that only
// populate EpisodeCount don't observe a behaviour change.
func (s Statistics) AiredMissing() int {
	aired := s.Aired
	if aired == 0 {
		aired = s.EpisodeCount
	}
	d := aired - s.EpisodeFileCount
	if d < 0 {
		return 0
	}
	return d
}

// Existing returns the count of episodes currently on disk for this
// scope (series-wide on a series.Statistics, season-wide on a
// season.Statistics). Wraps EpisodeFileCount for symmetry with Total /
// Aired so callers don't mix-and-match accessors and raw fields.
func (s Statistics) Existing() int { return s.EpisodeFileCount }
