package series

import "time"

type Episode struct {
	ID            int
	Number        int
	SeasonNumber  int
	Title         string
	Monitored     bool
	HasFile       bool
	AirDateUTC    time.Time
	QualityID     int
	QualityName   string
	EpisodeFileID int
}

func (e Episode) Aired(now time.Time) bool {
	if e.AirDateUTC.IsZero() {
		return false
	}
	return !e.AirDateUTC.After(now)
}
