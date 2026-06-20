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
