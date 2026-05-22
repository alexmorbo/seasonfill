package series

type SeriesType string

const (
	SeriesTypeStandard SeriesType = "standard"
	SeriesTypeAnime    SeriesType = "anime"
	SeriesTypeDaily    SeriesType = "daily"
)

type Series struct {
	ID             int
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
type Statistics struct {
	EpisodeCount     int
	EpisodeFileCount int
}

// AiredMissing returns aired-but-not-on-disk count, clamped to 0
// (Sonarr can return inconsistent snapshots mid-import).
func (s Statistics) AiredMissing() int {
	d := s.EpisodeCount - s.EpisodeFileCount
	if d < 0 {
		return 0
	}
	return d
}
