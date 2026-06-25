package tmdb

// GenreListResponse is the JSON shape of /3/genre/tv/list. The TMDB
// payload is `{"genres":[{"id":int,"name":string}, …]}`. Consumed by
// the discovery genre_sync loop (story 540 / B-49).
type GenreListResponse struct {
	Genres []GenreListItem `json:"genres"`
}

// GenreListItem is one row of GenreListResponse.Genres. ID is the TMDB
// canonical genre id (stable across languages); Name is the localised
// label.
type GenreListItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
