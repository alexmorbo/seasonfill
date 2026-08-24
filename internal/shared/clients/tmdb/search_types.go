package tmdb

// search_types.go — raw JSON shapes for GET /search/collection and
// /search/person (ADR-0024 S1.3). Mirror of discover_types.go /
// discover_movie_types.go: {page,results,total_pages,total_results} envelope,
// snake_case tags, no business logic. known_for[] on person results is
// intentionally NOT typed (v1 ignores it — ADR F-08 scope: names/titles only).

// CollectionListEntry is one /search/collection result row.
type CollectionListEntry struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	Overview     string `json:"overview"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	Adult        bool   `json:"adult"`
}

// CollectionListResponse is the paginated /search/collection envelope.
type CollectionListResponse struct {
	Page         int                   `json:"page"`
	Results      []CollectionListEntry `json:"results"`
	TotalPages   int                   `json:"total_pages"`
	TotalResults int                   `json:"total_results"`
}

// PersonListEntry is one /search/person result row. known_for[] is ignored
// in v1 (ADR blessed-assumption #4 — titles/names only, no filmography match).
type PersonListEntry struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	OriginalName       string  `json:"original_name"`
	ProfilePath        string  `json:"profile_path"`
	KnownForDepartment string  `json:"known_for_department"`
	Popularity         float64 `json:"popularity"`
	Adult              bool    `json:"adult"`
}

// PersonListResponse is the paginated /search/person envelope.
type PersonListResponse struct {
	Page         int               `json:"page"`
	Results      []PersonListEntry `json:"results"`
	TotalPages   int               `json:"total_pages"`
	TotalResults int               `json:"total_results"`
}
