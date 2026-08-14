package dto

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// MovieOverviewResponse is the localized-text payload returned by
// GET /api/v1/movies/:tmdb_id/overview?lang=. The movie analog of the
// SeriesOverviewResponse split (Story 529) — carries the movie's localized
// title/overview/tagline (split out of the base MovieDetailResponse) plus the
// served_language + degraded signal shared by the other movie sub-endpoints.
// No keywords/awards (those are series-only).
type MovieOverviewResponse struct {
	// TMDBID is the TMDB movie id from the URL.
	TMDBID domain.TMDBID `json:"tmdb_id" example:"603"`
	// Lang is the BCP-47 language requested.
	Lang string `json:"lang" example:"en-US"`
	// Title is the localized title (localized > canon fallback). Never empty.
	Title string `json:"title" example:"The Matrix"`
	// Overview is the localized synopsis. nil when no localized/canon overview
	// row exists in any language.
	Overview *string `json:"overview,omitempty"`
	// Tagline is the localized tagline. nil when absent.
	Tagline *string `json:"tagline,omitempty"`
	// ServedLanguage is the BCP-47 language the localized title resolved to
	// (the movie's principal localized text). Empty when no localized title row
	// exists. When it differs from Lang, Degraded includes "missing_lang".
	ServedLanguage string `json:"served_language,omitempty" example:"ru-RU"`
	// Degraded carries "missing_lang" when a real fallback-language title was
	// served. Empty slice on the happy path.
	Degraded []string `json:"degraded"`
}
