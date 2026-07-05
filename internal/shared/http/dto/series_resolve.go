package dto

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// SeriesResolveResponse is the 200 body returned by
// GET /api/v1/series/resolve?tmdb_id=<int>.
//
// SeriesID — the canonical series.id for the requested tmdb_id. When a
// canon row already exists it is returned as-is (no write); an unknown
// tmdb_id gets a freshly-created canon stub (hydration='stub') whose
// enrichment is enqueued at PriorityHot so a subsequent detail render
// lands on hydrated data. Lets the unified series card route every TV
// row (even person-page "other credits" with only a tmdb_id) to the
// internal /series/:id page instead of escaping to TMDB.
type SeriesResolveResponse struct {
	SeriesID domain.SeriesID `json:"series_id" example:"42"`
}
