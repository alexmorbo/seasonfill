package evaluate

import (
	"sort"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
)

type RankInput struct {
	Releases          []release.Release
	Missing           []int
	OriginGUID        string
	OriginIndexerName string
	OriginBonus       float64
}

func Rank(in RankInput) []release.Scored {
	scored := make([]release.Scored, 0, len(in.Releases))
	for _, r := range in.Releases {
		isOrigin := false
		if in.OriginGUID != "" && r.GUID == in.OriginGUID {
			isOrigin = true
		}
		if !isOrigin && in.OriginIndexerName != "" && r.IndexerName == in.OriginIndexerName {
			isOrigin = true
		}
		s := release.Scored{
			Release:          r,
			Coverage:         r.Coverage(in.Missing),
			IsOriginRelease:  isOrigin,
			OriginBonusValue: in.OriginBonus,
		}
		scored = append(scored, s)
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Less(scored[j])
	})
	return scored
}
