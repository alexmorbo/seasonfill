// Package rest ships the HTTP surface for the universal-search bounded
// context (ADR-0024 S1.4): GET /api/v1/search. Distinct package from
// internal/discovery/rest (F-11) — this is the unified hybrid search that
// returns a grouped envelope across four entities.
//
// dto.go declares the wire DTOs. Besides stdlib it imports the search
// domain package for the source consts (wireSource) — a legal
// interface→domain reference. The handler converts searchdomain hits →
// these structs at projection time; the use case / repository never see
// them.
//
// snake_case json tags match the repo convention (see
// internal/shared/http/dto/dto.go + internal/discovery/rest/types.go).
// Nullable fields are pointers and are emitted WITHOUT omitempty so the FE
// (Фаза 2/3) sees a stable shape — every key present, null when absent.
package rest

import (
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
)

// Provenance discriminators on every search hit (D10). The domain stamps
// searchdomain.SourceLibrary / SourceCatalog on each hit; wireSource maps the
// domain value onto these wire consts (both used → no unused symbol), defaulting
// empty/unknown to "library" so an un-stamped hit stays S1.4-identical.
const (
	sourceLibrary = "library"
	sourceCatalog = "catalog"
)

// wireSource maps a domain Source value onto the wire vocabulary.
func wireSource(domainSource string) string {
	switch domainSource {
	case searchdomain.SourceCatalog:
		return sourceCatalog
	default:
		return sourceLibrary
	}
}

// SearchSeriesItem is one library series hit. id is the internal SeriesID;
// tmdb_id is the public TMDB id when the row carries one (nullable).
type SearchSeriesItem struct {
	ID           int64   `json:"id"`
	TMDBID       *int64  `json:"tmdb_id"`
	Title        string  `json:"title"`
	Year         *int    `json:"year"`
	PosterPath   *string `json:"poster_path"`
	BackdropPath *string `json:"backdrop_path"`
	Source       string  `json:"source"`
}

// SearchMovieItem is one library movie hit. Same shape as the series item;
// id is the internal MovieID.
type SearchMovieItem struct {
	ID           int64   `json:"id"`
	TMDBID       *int64  `json:"tmdb_id"`
	Title        string  `json:"title"`
	Year         *int    `json:"year"`
	PosterPath   *string `json:"poster_path"`
	BackdropPath *string `json:"backdrop_path"`
	Source       string  `json:"source"`
}

// SearchCollectionItem is one library collection hit. id is the internal
// CollectionID; tmdb_id carries the external tmdb_collection_id.
type SearchCollectionItem struct {
	ID           int64   `json:"id"`
	TMDBID       *int64  `json:"tmdb_id"`
	Name         string  `json:"name"`
	PosterPath   *string `json:"poster_path"`
	BackdropPath *string `json:"backdrop_path"`
	Source       string  `json:"source"`
}

// SearchPersonItem is one library-restricted person hit (D7). id is the
// internal PersonID; known_for is the known_for_department string.
type SearchPersonItem struct {
	ID          int64   `json:"id"`
	TMDBID      *int64  `json:"tmdb_id"`
	Name        string  `json:"name"`
	ProfilePath *string `json:"profile_path"`
	KnownFor    *string `json:"known_for"`
	Source      string  `json:"source"`
}

// SearchResponse is the grouped envelope for GET /api/v1/search (D10). The
// four groups always serialize as arrays (never null) even when empty — the
// FE consumes arrays. query/scope/types echo the effective request so the FE
// can render the active state without re-parsing its own URL.
//
// S1.4 populates the groups from the library layer only; S1.3 will merge
// TMDB catalog hits into the SAME four groups (dedup by tmdb_id), so this
// shape is stable across that change.
type SearchResponse struct {
	Query       string                 `json:"query"`
	Scope       string                 `json:"scope"`
	Types       []string               `json:"types"`
	Series      []SearchSeriesItem     `json:"series"`
	Movies      []SearchMovieItem      `json:"movies"`
	Collections []SearchCollectionItem `json:"collections"`
	People      []SearchPersonItem     `json:"people"`
}
