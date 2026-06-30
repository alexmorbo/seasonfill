package values

import "errors"

var (
	ErrLanguageTagInvalid   = errors.New("values: language_tag must be BCP-47 (e.g. ru-RU)")
	ErrTitleEmpty           = errors.New("values: title value must be non-empty")
	ErrTaglineEmpty         = errors.New("values: tagline value must be non-empty")
	ErrYearInvalid          = errors.New("values: year must be in [1900,2100]")
	ErrMinutesInvalid       = errors.New("values: minutes must be > 0")
	ErrScoreInvalid         = errors.New("values: score must be in (0,10]")
	ErrVoteCountInvalid     = errors.New("values: vote_count must be >= 0")
	ErrLangCodeInvalid      = errors.New("values: lang_code must be ISO 639-1 (2 lowercase letters)")
	ErrCountryCodeInvalid   = errors.New("values: country_code must be ISO 3166-1 alpha-2 (2 uppercase letters)")
	ErrNextEpisodeInvalid   = errors.New("values: next_episode_canon requires non-zero season and episode")
	ErrContentRatingInvalid = errors.New("values: content_rating not in allowed set")
	ErrMediaHashInvalid     = errors.New("values: media_hash must be 64-char lowercase hex")
	ErrTrailerKeyInvalid    = errors.New("values: trailer_key must be 11-char YouTube key")
	ErrSeriesStatusInvalid  = errors.New("values: series_status not in allowed set")
)
