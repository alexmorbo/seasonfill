package tmdb

// MovieListEntry is the shared movie summary shape from /discover/movie,
// /trending/movie/{window}, /movie/popular, /search/movie. All four share the
// {page,results,total_pages,total_results} envelope. Movie analog of
// TVListEntry (discover_types.go). The only per-row difference vs TV is
// title/release_date vs name/first_air_date.
type MovieListEntry struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	OriginalTitle    string  `json:"original_title"`
	OriginalLanguage string  `json:"original_language"`
	Overview         string  `json:"overview"`
	PosterPath       string  `json:"poster_path"`
	BackdropPath     string  `json:"backdrop_path"`
	ReleaseDate      string  `json:"release_date"`
	VoteAverage      float64 `json:"vote_average"`
	VoteCount        int     `json:"vote_count"`
	Popularity       float64 `json:"popularity"`
	GenreIDs         []int   `json:"genre_ids"`
	Adult            bool    `json:"adult"`
}

// MovieListResponse is the paginated envelope shared by the four movie list
// endpoints. Pagination is 1-based; TotalPages caps at 500 on TMDB.
type MovieListResponse struct {
	Page         int              `json:"page"`
	Results      []MovieListEntry `json:"results"`
	TotalPages   int              `json:"total_pages"`
	TotalResults int              `json:"total_results"`
}

// MovieDiscoverFilter is the allow-listed /discover/movie param set. Movie
// analog of DiscoverFilter; note first_air_date.* → primary_release_date.*
// and the movie-only with_release_type / primary_release_year. include_adult
// is hardcoded false in buildMovieDiscoverQuery.
//
// Field semantics mirror DiscoverFilter: nil pointer / empty slice → param
// omitted; multi-value slices join with comma (AND) except WithReleaseType
// which OR-joins with pipe (TMDB set-membership for release types).
type MovieDiscoverFilter struct {
	WithGenres            []int
	WithoutGenres         []int
	PrimaryReleaseDateGte *string  // primary_release_date.gte=2016-01-01
	PrimaryReleaseDateLte *string  // primary_release_date.lte=2026-12-31
	PrimaryReleaseYear    *int     // primary_release_year=2024
	VoteAverageGte        *float64 // vote_average.gte=7.5
	VoteAverageLte        *float64 // vote_average.lte=10
	VoteCountGte          *int     // vote_count.gte=200
	WithRuntimeGte        *int     // with_runtime.gte=20
	WithRuntimeLte        *int     // with_runtime.lte=240
	WithOriginalLang      *string  // with_original_language=ja
	WithOriginCountry     *string  // with_origin_country=JP
	WithKeywords          []int    // with_keywords=210024
	WithoutKeywords       []int    // without_keywords=210024
	WithWatchProviders    []int    // with_watch_providers=8
	WatchRegion           *string  // watch_region=US
	WithReleaseType       []int    // with_release_type=2|3 (OR-joined; 1..6)
	SortBy                string   // popularity.desc | vote_average.desc | primary_release_date.desc | primary_release_date.asc | revenue.desc
}

// TMDB Discover Movie `with_release_type` enum (1..6):
//   1 — Premiere
//   2 — Theatrical (limited)
//   3 — Theatrical
//   4 — Digital
//   5 — Physical
//   6 — TV
//
// The handler F-3 validator clamps these to 1..6; values outside are
// silently 422'd by TMDB.
