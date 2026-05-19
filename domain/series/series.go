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
