package dto

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// MovieRecommendationsResponse is the payload for
// GET /api/v1/movies/:tmdb_id/recommendations. The movie analog of
// SeriesRecommendationsResponse (dto/series_detail.go) — no per-instance
// membership fields and no in_library probe in v1 (parity with MovieCastResponse,
// which also defers in_library).
type MovieRecommendationsResponse struct {
	// TMDBID is the base movie's TMDB id (from the URL).
	TMDBID domain.TMDBID `json:"tmdb_id" example:"603"`
	// Items is the rank-ordered recs slice (empty when the movie has none).
	Items []MovieRecommendation `json:"items"`
	// TotalCount is the number of RENDERABLE recs (canon-resolvable + tmdb-linkable),
	// NOT the raw join-row count.
	TotalCount int  `json:"total_count" example:"12"`
	HasMore    bool `json:"has_more"`
	Limit      int  `json:"limit" example:"20"`
	Offset     int  `json:"offset" example:"0"`
	// Degraded carries "tmdb_movie" when the recs list read failed (empty items,
	// still 200). Empty slice on the happy path.
	Degraded []string `json:"degraded"`
}

// MovieRecommendation is one "you might also like" tile. TMDBID is the FE
// navigation target (→ /movies/:tmdb_id). PosterAsset is the resolved media hash
// (nil → frontend renders a monogram).
type MovieRecommendation struct {
	TMDBID      domain.TMDBID `json:"tmdb_id" example:"604"`
	Title       string        `json:"title" example:"The Matrix Reloaded"`
	Year        *int          `json:"year,omitempty" example:"2003"`
	PosterAsset *string       `json:"poster_asset,omitempty"`
	TMDBRating  *float64      `json:"tmdb_rating,omitempty" example:"7.2"`
}
