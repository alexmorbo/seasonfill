// movie_types.go declares the wire DTOs for the movie discovery HTTP surface
// (Ф6-R-4a). Movie analog of the DiscoverySeriesItem / DiscoverResponse pair;
// movie-shaped (movie_id + no TVDB/in-library — in-library membership is
// R-4b/R-6, out of scope for R-4a). Poster/backdrop carry the resolved sha256
// wire hash when a MediaResolver is wired, else the raw TMDB path.
package rest

// MovieDiscoverItem is one row of GET /api/v1/discovery/movie/* responses.
//
// MovieID is the local `movies` primary key. TMDBID is the public TMDB id.
// PosterHash / BackdropHash carry the sha256-hex content address the FE feeds
// into /api/v1/media/:hash (mirror of the series *_hash rename); when no
// resolver is wired they carry the raw TMDB path.
type MovieDiscoverItem struct {
	MovieID          int64    `json:"movie_id"`
	TMDBID           *int     `json:"tmdb_id,omitempty"`
	Title            string   `json:"title"`
	Year             *int     `json:"year,omitempty"`
	PosterHash       *string  `json:"poster_hash,omitempty"`
	BackdropHash     *string  `json:"backdrop_hash,omitempty"`
	OriginalLanguage *string  `json:"original_language,omitempty"`
	TMDBRating       *float64 `json:"tmdb_rating,omitempty"`
}

// MovieDiscoverResponse is the wire envelope for the movie discovery
// endpoints. Mirrors DiscoverResponse: cache_status ∈ {hit,miss,warming};
// degraded folds tmdb_throttled; retry_after_seconds is set on the warming
// (202) branch.
type MovieDiscoverResponse struct {
	Items             []MovieDiscoverItem `json:"items"`
	Page              int                 `json:"page"`
	PerPage           int                 `json:"per_page"`
	CacheStatus       string              `json:"cache_status"`
	Degraded          []string            `json:"degraded,omitempty"`
	RetryAfterSeconds int                 `json:"retry_after_seconds,omitempty"`
}
