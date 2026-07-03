package tmdb

// SeasonResponse is the raw JSON shape of GET /tv/{id}/season/{n}.
// Episodes[] is the per-episode payload — each row carries guest
// stars + per-episode crew, which the mapper splits into
// EpisodeCredit rows.
type SeasonResponse struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Overview     string          `json:"overview"`
	AirDate      string          `json:"air_date"`
	SeasonNumber int             `json:"season_number"`
	PosterPath   string          `json:"poster_path"`
	Episodes     []SeasonEpisode `json:"episodes"`

	// append_to_response sub-resource (S-C). Nilable — callers MUST treat a
	// missing array as empty.
	Translations *SeasonTranslations `json:"translations"`

	// append_to_response=images sub-resource (S-C2). Nilable — callers MUST
	// treat a missing object as empty. Posters only (TMDB season images carry
	// no backdrops).
	Images *SeasonImages `json:"images"`
}

// SeasonEpisode mirrors episodes[*] on the season payload.
type SeasonEpisode struct {
	ID            int64              `json:"id"`
	Name          string             `json:"name"`
	Overview      string             `json:"overview"`
	AirDate       string             `json:"air_date"`
	EpisodeNumber int                `json:"episode_number"`
	SeasonNumber  int                `json:"season_number"`
	Runtime       *int               `json:"runtime"`
	EpisodeType   string             `json:"episode_type"`
	VoteAverage   float64            `json:"vote_average"`
	VoteCount     int                `json:"vote_count"`
	StillPath     string             `json:"still_path"`
	GuestStars    []SeasonGuestStar  `json:"guest_stars"`
	Crew          []SeasonCrewMember `json:"crew"`
}

// SeasonGuestStar mirrors episode.guest_stars[*]. CreditID is the
// canonical id; PersonID resolves via people.tmdb_id lookup.
type SeasonGuestStar struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	OriginalName       string  `json:"original_name"`
	Gender             *int    `json:"gender"`
	KnownForDepartment string  `json:"known_for_department"`
	Popularity         float64 `json:"popularity"`
	ProfilePath        string  `json:"profile_path"`
	CreditID           string  `json:"credit_id"`
	Character          string  `json:"character"`
	Order              int     `json:"order"`
}

// SeasonCrewMember mirrors episode.crew[*]. EpisodeCredit row
// emits Department + Job.
type SeasonCrewMember struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	OriginalName       string  `json:"original_name"`
	Gender             *int    `json:"gender"`
	KnownForDepartment string  `json:"known_for_department"`
	Popularity         float64 `json:"popularity"`
	ProfilePath        string  `json:"profile_path"`
	CreditID           string  `json:"credit_id"`
	Department         string  `json:"department"`
	Job                string  `json:"job"`
}

// SeasonTranslations — append_to_response=translations sub-resource on
// /tv/{id}/season/{n}. Each entry carries one language's localised
// name/overview for the SEASON (NOT its episodes — see S-C O-4). Mirrors
// TVTranslations; the `data` object is season-scoped (name + overview only).
type SeasonTranslations struct {
	Translations []SeasonTranslation `json:"translations"`
}

// SeasonTranslation is one row of translations.translations[*]. ISO6391 is the
// bare 2-letter language code (matched against shortLang(userTag)); Data holds
// the localised season text fields.
type SeasonTranslation struct {
	ISO6391  string                `json:"iso_639_1"`
	ISO31661 string                `json:"iso_3166_1"`
	Data     SeasonTranslationData `json:"data"`
}

// SeasonTranslationData is the `data` object inside a SeasonTranslation.
// Season translations expose only name + overview (unlike TV translations,
// which also carry tagline/homepage).
type SeasonTranslationData struct {
	Name     string `json:"name"`
	Overview string `json:"overview"`
}

// SeasonImages — append_to_response=images sub-resource on /tv/{id}/season/{n}
// (S-C2). TMDB season images expose ONLY posters (no backdrops/logos). Each
// entry carries iso_639_1 for per-language selection (matched via shortLang).
// Reuses TVImage (same package, tv_types.go).
type SeasonImages struct {
	Posters []TVImage `json:"posters"`
}
