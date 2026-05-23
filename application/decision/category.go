// Package decision (application layer) classifies decision reasons into
// UI-facing Category values. Single source of truth — frontend renders off
// Category, never raw Reason.
package decision

import "github.com/alexmorbo/seasonfill/domain/decision"

type Category string

const (
	CategoryAllComplete   Category = "all_complete"
	CategorySonarrHandles Category = "sonarr_handles"
	CategoryActionTaken   Category = "action_taken"
	CategoryBlocked       Category = "blocked"
	CategoryNothingFound  Category = "nothing_found"
	CategoryError         Category = "error"
	CategoryUnknown       Category = "unknown"
)

// reasonCategory — explicit, exhaustive map keyed off typed Reason. DO NOT
// substring-match: reason strings overlap (e.g.
// "release_covers_no_missing_episodes" contains "_no_missing"). The drift
// test in category_test.go fails when a new Reason isn't classified here.
var reasonCategory = map[decision.Reason]Category{
	decision.ReasonGrabSelected:              CategoryActionTaken,
	decision.ReasonGrabSelectedDryRun:        CategoryActionTaken,
	decision.ReasonSkipNoMissing:             CategoryAllComplete,
	decision.ReasonSkipUnmonitoredSeason:     CategoryAllComplete,
	decision.ReasonSkipSpecials:              CategoryAllComplete,
	decision.ReasonSkipAnime:                 CategoryAllComplete,
	decision.ReasonSkipFullMissing:           CategorySonarrHandles,
	decision.ReasonSkipSeriesCooldown:        CategoryBlocked,
	decision.ReasonSkipMaxGrabsReached:       CategoryBlocked,
	decision.ReasonFilterGUIDCooldown:        CategoryBlocked,
	decision.ReasonSkipNoCandidates:          CategoryNothingFound,
	decision.ReasonSkipNoReleases:            CategoryNothingFound,
	decision.ReasonFilterCoversNothing:       CategoryNothingFound,
	decision.ReasonFilterQualityNotInProfile: CategoryNothingFound,
	decision.ReasonFilterQualityDowngrade:    CategoryNothingFound,
	decision.ReasonFilterRejectionsUnsafe:    CategoryNothingFound,
	decision.ReasonFilterCFScoreBelowMin:     CategoryNothingFound,
	decision.ReasonFilterAirDateNotReady:     CategoryNothingFound,
	decision.ReasonErrorFetchReleases:        CategoryError,
	decision.ReasonErrorFetchEpisodes:        CategoryError,
	decision.ReasonErrorFetchEpisodeFiles:    CategoryError,
	decision.ReasonErrorFetchQualityProfile:  CategoryError,
	decision.ReasonFilterUnknownSeries:       CategoryError,
}

// Classify maps a raw reason to a UI category. Empty input or unmapped
// reason → CategoryUnknown (unreachable when reasonCategory is exhaustive).
func Classify(reason string) Category {
	if reason == "" {
		return CategoryUnknown
	}
	if c, ok := reasonCategory[decision.Reason(reason)]; ok {
		return c
	}
	return CategoryUnknown
}
