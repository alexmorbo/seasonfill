package dto

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// MovieCastResponse is the full cast payload returned by
// GET /api/v1/movies/:tmdb_id/cast?lang=&sort=. The movie analog of
// SeriesCastResponse (dto/cast.go) — no crew tab, no episode/last-appearance
// aggregates (movie person_credits carry none) and no in_library probe in v1.
type MovieCastResponse struct {
	// TMDBID is the TMDB movie id from the URL.
	TMDBID domain.TMDBID `json:"tmdb_id" example:"603"`
	// Lang is the BCP-47 language requested.
	Lang string `json:"lang" example:"en-US"`
	// Cast is the cast list. Default order is credit_order ASC NULLS LAST
	// (?sort=name switches to localized display-name collation). Empty slice
	// when the movie has no person_credits kind='cast' rows.
	Cast []MovieCastMember `json:"cast"`
	// ServedLanguage is the BCP-47 language the localized title (the movie's
	// principal localized text) was served in. Empty when no localized title
	// row exists. When it differs from Lang, Degraded includes "missing_lang".
	ServedLanguage string `json:"served_language,omitempty" example:"ru-RU"`
	// Degraded carries "missing_lang" when a real fallback-language title was
	// served. Empty slice on the happy path.
	Degraded []string `json:"degraded"`
}

// MovieCastMember is one cast row. Mirrors CastPageMember minus the
// series-only fields (episode_count, last_appearance_season, in_library).
type MovieCastMember struct {
	// PersonID is the canon people.id (person-page link target).
	PersonID int64 `json:"person_id"`
	// TMDBID is the TMDB person id. nil for non-TMDB-sourced people (rare).
	TMDBID *domain.TMDBID `json:"tmdb_id,omitempty"`
	// Name is the resolved display name (localized via people_texts fallback).
	Name string `json:"name" example:"Keanu Reeves"`
	// ProfileAsset is the media_assets hash for the profile photo. nil when
	// the person has no profile_path (frontend renders a monogram).
	ProfileAsset *string `json:"profile_asset,omitempty"`
	// CharacterName is the role in this movie. nil when TMDB carried none.
	CharacterName *string `json:"character_name,omitempty"`
	// CreditOrder is the TMDB billing order. nil sorts NULLS LAST.
	CreditOrder *int `json:"credit_order,omitempty"`
}
