package decision

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReason_Constants(t *testing.T) {
	t.Parallel()

	// Spot-check a representative subset across the grab/skip/error/filter families.
	tests := map[Reason]string{
		ReasonGrabSelected:              "grab_selected",
		ReasonGrabSelectedDryRun:        "grab_selected_dry_run",
		ReasonSkipNoMissing:             "skip_no_missing_episodes",
		ReasonSkipFullMissing:           "skip_all_episodes_missing",
		ReasonSkipUnmonitoredSeason:     "skip_unmonitored_season",
		ReasonSkipSpecials:              "skip_specials_season",
		ReasonSkipAnime:                 "skip_anime_series",
		ReasonSkipNoCandidates:          "skip_no_candidates_after_filter",
		ReasonSkipNoReleases:            "skip_no_releases_returned",
		ReasonErrorFetchReleases:        "error_fetch_releases",
		ReasonErrorFetchEpisodes:        "error_fetch_episodes",
		ReasonErrorFetchEpisodeFiles:    "error_fetch_episode_files",
		ReasonErrorFetchQualityProfile:  "error_fetch_quality_profile",
		ReasonFilterUnknownSeries:       "unknown_series_mapping",
		ReasonFilterCoversNothing:       "release_covers_no_missing_episodes",
		ReasonFilterQualityNotInProfile: "quality_not_in_profile",
		ReasonFilterQualityDowngrade:    "would_downgrade_existing_quality",
		ReasonFilterRejectionsUnsafe:    "rejection_not_in_safe_list",
		ReasonFilterCFScoreBelowMin:     "custom_format_score_below_minimum",
		ReasonFilterAirDateNotReady:     "release_partial_and_require_all_aired",
	}

	for reason, want := range tests {
		assert.Equal(t, want, string(reason))
	}
}
