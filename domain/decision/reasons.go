package decision

type Reason string

const (
	ReasonGrabSelected              Reason = "grab_selected"
	ReasonGrabSelectedDryRun        Reason = "grab_selected_dry_run"
	ReasonSkipNoMissing             Reason = "skip_no_missing_episodes"
	ReasonSkipFullMissing           Reason = "skip_all_episodes_missing"
	ReasonSkipUnmonitoredSeason     Reason = "skip_unmonitored_season"
	ReasonSkipSpecials              Reason = "skip_specials_season"
	ReasonSkipAnime                 Reason = "skip_anime_series"
	ReasonSkipNoCandidates          Reason = "skip_no_candidates_after_filter"
	ReasonSkipNoReleases            Reason = "skip_no_releases_returned"
	ReasonErrorFetchReleases        Reason = "error_fetch_releases"
	ReasonErrorFetchEpisodes        Reason = "error_fetch_episodes"
	ReasonErrorFetchEpisodeFiles    Reason = "error_fetch_episode_files"
	ReasonErrorFetchQualityProfile  Reason = "error_fetch_quality_profile"
	ReasonFilterUnknownSeries       Reason = "unknown_series_mapping"
	ReasonFilterCoversNothing       Reason = "release_covers_no_missing_episodes"
	ReasonFilterQualityNotInProfile Reason = "quality_not_in_profile"
	ReasonFilterQualityDowngrade    Reason = "would_downgrade_existing_quality"
	ReasonFilterRejectionsUnsafe    Reason = "rejection_not_in_safe_list"
	ReasonFilterCFScoreBelowMin     Reason = "custom_format_score_below_minimum"
	ReasonFilterAirDateNotReady     Reason = "release_partial_and_require_all_aired"
)
