package dto

// SeriesRatingsResponse is the FLAT ratings payload for
// GET /api/v1/series/:id/ratings (W18-7a). It is DELIBERATELY distinct from
// the canonical nested RatingScore{score,votes} used inside SeriesDetailResponse
// (series_detail.go): the /ratings endpoint is a stale-while-revalidate surface
// with a per-source freshness status, so a flat single-object shape keeps the FE
// hook (useSeriesRatings, W18-7b) trivial and the openapi schema stable.
//
// `Rated` is the OMDb content-rating (omdb_rated), NOT the TMDB ContentRatingBadge
// (content_ratings[]) — different sources; the FE must not conflate them.
//
// Every value field is an omitempty pointer: absent ⇒ nothing to show for it. The
// Sources block is ALWAYS present so the FE can decide whether to re-poll.
type SeriesRatingsResponse struct {
	TMDBRating *float64             `json:"tmdb_rating,omitempty"`
	TMDBVotes  *int                 `json:"tmdb_votes,omitempty"`
	IMDBRating *float64             `json:"imdb_rating,omitempty"`
	IMDBVotes  *int                 `json:"imdb_votes,omitempty"`
	Rated      *string              `json:"rated,omitempty"`
	Awards     *string              `json:"awards,omitempty"`
	Sources    SeriesRatingsSources `json:"sources"`
}

// SeriesRatingsSources carries the per-source freshness status.
type SeriesRatingsSources struct {
	TMDB string `json:"tmdb"`
	OMDb string `json:"omdb"`
}

// Per-source freshness status vocabulary (W18-7a).
const (
	RatingStatusFresh        = "fresh"        // present + within TTL, no fetch
	RatingStatusRevalidating = "revalidating" // present + stale, OLD value returned + BG refresh kicked
	RatingStatusPending      = "pending"      // empty + fetch in flight / budget-exhausted / terminal
	RatingStatusUnavailable  = "unavailable"  // no id, or empty-but-freshly-synced (genuine N/A)
)
