package tmdb

// FindResponse mirrors GET /find/{external_id}. Only TVResults is
// consumed by C-2; the other arrays stay here as forward-compat.
type FindResponse struct {
	MovieResults  []FindMovieResult  `json:"movie_results"`
	TVResults     []FindTVResult     `json:"tv_results"`
	PersonResults []FindPersonResult `json:"person_results"`
}

// FindTVResult is one row of tv_results[*]. C-2 reads only `ID`.
type FindTVResult struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	OriginalName  string   `json:"original_name"`
	FirstAirDate  string   `json:"first_air_date"`
	OriginCountry []string `json:"origin_country"`
	PosterPath    string   `json:"poster_path"`
	BackdropPath  string   `json:"backdrop_path"`
	VoteAverage   float64  `json:"vote_average"`
	VoteCount     int      `json:"vote_count"`
}

// FindMovieResult — placeholder for forward-compat.
type FindMovieResult struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	ReleaseDate string  `json:"release_date"`
	PosterPath  string  `json:"poster_path"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
}

// FindPersonResult — placeholder for forward-compat.
type FindPersonResult struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	KnownForDepartment string `json:"known_for_department"`
	ProfilePath        string `json:"profile_path"`
}
