package dto

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// SeriesRefreshResponse is the 202 body returned by
// POST /api/v1/instances/:name/series/:id/refresh (story 218 E-2).
//
// SeriesID — the resolved canon series.id.
// SeriesQueued — whether the series enrichment was enqueued
//
//	(always true on a 202; reserved for future "already pending"
//	reporting).
//
// Persons — number of person-enrichment jobs queued (0..10).
// OMDbQueued — true iff the canon row carries a non-empty imdb_id.
type SeriesRefreshResponse struct {
	SeriesID     domain.SeriesID `json:"series_id"      example:"42"`
	SeriesQueued bool            `json:"series_queued"  example:"true"`
	Persons      int             `json:"persons_queued" example:"10"`
	OMDbQueued   bool            `json:"omdb_queued"    example:"true"`
}
