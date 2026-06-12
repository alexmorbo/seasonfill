package tmdb

// PersonResponse mirrors GET /person/{id} with
// append_to_response=tv_credits,movie_credits,external_ids.
type PersonResponse struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	OriginalName       string   `json:"original_name"`
	AlsoKnownAs        []string `json:"also_known_as"`
	Biography          string   `json:"biography"`
	Birthday           string   `json:"birthday"`
	Deathday           string   `json:"deathday"`
	Gender             *int     `json:"gender"`
	PlaceOfBirth       string   `json:"place_of_birth"`
	KnownForDepartment string   `json:"known_for_department"`
	Popularity         float64  `json:"popularity"`
	ProfilePath        string   `json:"profile_path"`
	IMDBID             string   `json:"imdb_id"`
	Homepage           string   `json:"homepage"`

	TVCredits    *PersonTVCredits    `json:"tv_credits"`
	MovieCredits *PersonMovieCredits `json:"movie_credits"`
	ExternalIDs  *PersonExternalIDs  `json:"external_ids"`
}

// PersonTVCredits mirrors tv_credits sub-resource.
type PersonTVCredits struct {
	Cast []PersonTVCredit `json:"cast"`
	Crew []PersonTVCredit `json:"crew"`
}

// PersonMovieCredits mirrors movie_credits sub-resource.
type PersonMovieCredits struct {
	Cast []PersonMovieCredit `json:"cast"`
	Crew []PersonMovieCredit `json:"crew"`
}

// PersonTVCredit is one row of tv_credits.{cast,crew}[*]. Fields
// differ between cast (Character) and crew (Department + Job),
// but the JSON shape is unified — empty strings on the inapplicable
// side.
type PersonTVCredit struct {
	ID           int64   `json:"id"`
	CreditID     string  `json:"credit_id"`
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Character    string  `json:"character"`
	Department   string  `json:"department"`
	Job          string  `json:"job"`
	EpisodeCount int     `json:"episode_count"`
	FirstAirDate string  `json:"first_air_date"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int     `json:"vote_count"`
}

// PersonMovieCredit is one row of movie_credits.{cast,crew}[*].
type PersonMovieCredit struct {
	ID            int64   `json:"id"`
	CreditID      string  `json:"credit_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Character     string  `json:"character"`
	Department    string  `json:"department"`
	Job           string  `json:"job"`
	ReleaseDate   string  `json:"release_date"`
	PosterPath    string  `json:"poster_path"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
}

// PersonExternalIDs mirrors external_ids sub-resource.
type PersonExternalIDs struct {
	IMDBID      string `json:"imdb_id"`
	FacebookID  string `json:"facebook_id"`
	InstagramID string `json:"instagram_id"`
	TwitterID   string `json:"twitter_id"`
	WikidataID  string `json:"wikidata_id"`
	TVDBID      *int64 `json:"tvdb_id"`
}
