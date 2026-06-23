package tmdb

// TVListEntry is the common TV summary shape returned by /trending/tv,
// /tv/popular, /discover/tv, and /search/tv. All four endpoints share the
// SAME envelope ({page, results, total_pages, total_results}) and the SAME
// per-row shape — the only field that varies is media_type on /trending/tv
// (which we ignore because Discovery is TV-only).
//
// Subset of TVResponse (no append_to_response sub-resources). The Discovery
// worker (story 506) maps these into domain.DiscoveryItem; story 504 ships
// only the raw types.
type TVListEntry struct {
	ID               int64    `json:"id"`
	Name             string   `json:"name"`
	OriginalName     string   `json:"original_name"`
	OriginalLanguage string   `json:"original_language"`
	Overview         string   `json:"overview"`
	PosterPath       string   `json:"poster_path"`
	BackdropPath     string   `json:"backdrop_path"`
	FirstAirDate     string   `json:"first_air_date"`
	VoteAverage      float64  `json:"vote_average"`
	VoteCount        int      `json:"vote_count"`
	Popularity       float64  `json:"popularity"`
	OriginCountry    []string `json:"origin_country"`
	GenreIDs         []int    `json:"genre_ids"`
	Adult            bool     `json:"adult"`
}

// TVListResponse is the paginated envelope. Pagination is 1-based.
// TotalPages caps at 500 on TMDB (any page > 500 returns 422 — the
// caller's F-3 validator in story 509 enforces the cap).
type TVListResponse struct {
	Page         int           `json:"page"`
	Results      []TVListEntry `json:"results"`
	TotalPages   int           `json:"total_pages"`
	TotalResults int           `json:"total_results"`
}

// TrendingScope is the typed enum for /trending/tv/{day|week}. Sized as
// a string so it goes straight into the URL without a separate switch.
type TrendingScope string

const (
	TrendingDay  TrendingScope = "day"
	TrendingWeek TrendingScope = "week"
)

// DiscoverFilter is the wire-format struct holding every allow-listed
// /discover/tv query parameter per PRD §5.1.2 lines 776-803. Plain
// struct — validation lives at the handler F-3 layer (story 509).
//
// Field semantics:
//   - Slice fields: empty/nil → param omitted from URL. Multi-value
//     joining uses comma (AND) by default — see WithStatusOp / WithTypeOp
//     for the pipe (OR) override on those two specific params.
//   - Pointer fields: nil → param omitted; non-nil → emitted even if
//     dereferences to a zero value (the caller wanted "exactly 0").
//   - String fields: empty → param omitted.
//   - `include_adult` is HARDCODED false in buildDiscoverQuery — not
//     a configurable filter field per PRD §5.1.2 line 768.
type DiscoverFilter struct {
	WithGenres         []int    // with_genres=18,35
	WithoutGenres      []int    // without_genres=10764
	FirstAirDateGte    *string  // first_air_date.gte=2016-01-01
	FirstAirDateLte    *string  // first_air_date.lte=2026-12-31
	VoteAverageGte     *float64 // vote_average.gte=7.5
	VoteAverageLte     *float64 // vote_average.lte=10
	VoteCountGte       *int     // vote_count.gte=200
	WithRuntimeGte     *int     // with_runtime.gte=20
	WithRuntimeLte     *int     // with_runtime.lte=120
	WithOriginalLang   *string  // with_original_language=ja
	WithNetworks       []int    // with_networks=213
	WithOriginCountry  *string  // with_origin_country=JP
	WithKeywords       []int    // with_keywords=210024
	WithWatchProviders []int    // with_watch_providers=8
	WatchRegion        *string  // watch_region=US
	WithStatus         []int    // with_status=0,1 (defaults to OR join per TMDB API)
	WithStatusOp       string   // "and" | "or" (default "or"; empty → "or")
	WithType           []int    // with_type=0,2
	WithTypeOp         string   // "and" | "or" (default "or"; empty → "or")
	SortBy             string   // popularity.desc | vote_average.desc | first_air_date.desc
}

// TMDB Discover TV `with_status` enum (0..5):
//   0 — Returning Series
//   1 — Planned
//   2 — In Production
//   3 — Ended
//   4 — Cancelled
//   5 — Pilot
//
// TMDB Discover TV `with_type` enum (0..6):
//   0 — Documentary
//   1 — News
//   2 — Miniseries
//   3 — Reality
//   4 — Scripted
//   5 — Talk Show
//   6 — Video
//
// Allow-list rejection in handler F-3 (story 509) MUST clamp these to the
// closed ranges above; values outside are silently 422'd by TMDB.
