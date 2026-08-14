package dto

// MovieRatingsResponse is the FLAT ratings payload for
// GET /api/v1/movies/:tmdb_id/ratings (Ф2.3) — the movie analog of
// SeriesRatingsResponse (series_ratings.go). It mirrors that DTO's field
// names/shape for FE hook parity: TMDB ★ + votes, IMDb ★ + votes, the OMDb
// content-rating (`rated`) and `awards`, plus a per-source freshness block.
//
// Unlike the series endpoint (a stale-while-revalidate surface), movie ratings
// are READ-ONLY from the canon row — no live TMDB/OMDb refresh is wired in the
// moviedetail vertical. The Sources statuses are therefore STATIC: a source is
// `fresh` when its value is present and `unavailable` when absent (there is
// nothing to revalidate). The block is retained so the FE ratings hook can be
// shared across the series and movie verticals unchanged.
//
// `Rated` is the OMDb content-rating (movies.omdb_rated), NOT a TMDB badge.
// There is deliberately NO Rotten Tomatoes / Metacritic field: those columns do
// not exist on the movies row, and the series ratings endpoint does not expose
// them either.
//
// Every value field is an omitempty pointer: absent ⇒ nothing to show for it.
// The Sources block is ALWAYS present.
type MovieRatingsResponse struct {
	TMDBRating *float64            `json:"tmdb_rating,omitempty"`
	TMDBVotes  *int                `json:"tmdb_votes,omitempty"`
	IMDBRating *float64            `json:"imdb_rating,omitempty"`
	IMDBVotes  *int                `json:"imdb_votes,omitempty"`
	Rated      *string             `json:"rated,omitempty"`
	Awards     *string             `json:"awards,omitempty"`
	Sources    MovieRatingsSources `json:"sources"`
}

// MovieRatingsSources carries the per-source freshness status. Mirrors
// SeriesRatingsSources. For movies the values are drawn from the shared
// RatingStatus* vocabulary but only ever `fresh` / `unavailable` (read-only).
type MovieRatingsSources struct {
	TMDB string `json:"tmdb"`
	OMDb string `json:"omdb"`
}
