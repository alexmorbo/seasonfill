package release

import "time"

type Release struct {
	GUID                 string
	Title                string
	IndexerID            int
	IndexerName          string
	IndexerPriority      int
	Protocol             string
	QualityID            int
	QualityName          string
	CustomFormatScore    int
	Seeders              int
	Leechers             int
	SizeBytes            int64
	MappedEpisodeNumbers []int
	MappedSeasonNumber   int
	Rejections           []string
	PublishedUTC         time.Time
	IsFullSeason         bool
}

func (r Release) HasRejection(name string) bool {
	for _, rej := range r.Rejections {
		if rej == name {
			return true
		}
	}
	return false
}

func (r Release) Coverage(missing []int) int {
	if len(r.MappedEpisodeNumbers) == 0 || len(missing) == 0 {
		return 0
	}
	mset := make(map[int]struct{}, len(missing))
	for _, n := range missing {
		mset[n] = struct{}{}
	}
	count := 0
	for _, n := range r.MappedEpisodeNumbers {
		if _, ok := mset[n]; ok {
			count++
		}
	}
	return count
}
