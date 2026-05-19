package evaluate

import (
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type FilterInput struct {
	Releases             []release.Release
	Missing              []int
	Have                 []series.Episode
	Episodes             []series.Episode
	Profile              ports.QualityProfile
	MinCustomFormatScore int
	RequireAllAired      bool
	NowUTC               time.Time
}

type FilterResult struct {
	Kept        []release.Release
	FilteredOut []decision.FilteredCandidate
}

var safeRejectionPrefixes = []string{
	"Existing file on disk has a equal or higher Custom Format score",
	"Existing file on disk is of equal or higher preference",
	"Full season pack",
}

func rejectionsAreSafe(rejs []string) (string, bool) {
	for _, rej := range rejs {
		if strings.EqualFold(rej, "Unknown Series") {
			return rej, false
		}
		ok := false
		for _, prefix := range safeRejectionPrefixes {
			if strings.HasPrefix(rej, prefix) {
				ok = true
				break
			}
		}
		if !ok {
			return rej, false
		}
	}
	return "", true
}

func qualityOrder(profile ports.QualityProfile, qualityID int) (int, bool) {
	for _, it := range profile.Items {
		if it.ID == qualityID {
			return it.Order, true
		}
	}
	return 0, false
}

func qualityAllowed(profile ports.QualityProfile, qualityID int) bool {
	_, ok := qualityOrder(profile, qualityID)
	return ok
}

func wouldDowngrade(profile ports.QualityProfile, rel release.Release, have []series.Episode) bool {
	relOrder, ok := qualityOrder(profile, rel.QualityID)
	if !ok {
		return false
	}
	for _, ep := range have {
		exOrder, ok := qualityOrder(profile, ep.QualityID)
		if !ok {
			continue
		}
		if relOrder < exOrder {
			return true
		}
	}
	return false
}

func Filter(in FilterInput) FilterResult {
	res := FilterResult{
		Kept:        make([]release.Release, 0, len(in.Releases)),
		FilteredOut: make([]decision.FilteredCandidate, 0, len(in.Releases)),
	}

	for _, r := range in.Releases {
		fc := decision.FilteredCandidate{
			GUID:       r.GUID,
			Title:      r.Title,
			Indexer:    r.IndexerName,
			Quality:    r.QualityName,
			Rejections: r.Rejections,
			Coverage:   r.Coverage(in.Missing),
		}

		if r.HasRejection("Unknown Series") {
			fc.Reason = string(decision.ReasonFilterUnknownSeries)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if fc.Coverage == 0 {
			fc.Reason = string(decision.ReasonFilterCoversNothing)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if !qualityAllowed(in.Profile, r.QualityID) {
			fc.Reason = string(decision.ReasonFilterQualityNotInProfile)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if wouldDowngrade(in.Profile, r, in.Have) {
			fc.Reason = string(decision.ReasonFilterQualityDowngrade)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if rej, ok := rejectionsAreSafe(r.Rejections); !ok {
			fc.Reason = string(decision.ReasonFilterRejectionsUnsafe) + ": " + rej
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if r.CustomFormatScore < in.MinCustomFormatScore {
			fc.Reason = string(decision.ReasonFilterCFScoreBelowMin)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		if in.RequireAllAired && hasUnairedMappedEpisode(r, in.Episodes, in.NowUTC) {
			fc.Reason = string(decision.ReasonFilterAirDateNotReady)
			res.FilteredOut = append(res.FilteredOut, fc)
			continue
		}

		res.Kept = append(res.Kept, r)
	}

	return res
}

func hasUnairedMappedEpisode(r release.Release, episodes []series.Episode, now time.Time) bool {
	if len(r.MappedEpisodeNumbers) == 0 || len(episodes) == 0 {
		return false
	}
	air := make(map[int]time.Time, len(episodes))
	for _, ep := range episodes {
		air[ep.Number] = ep.AirDateUTC
	}
	for _, n := range r.MappedEpisodeNumbers {
		date, ok := air[n]
		if !ok || date.IsZero() {
			continue
		}
		if date.After(now) {
			return true
		}
	}
	return false
}
