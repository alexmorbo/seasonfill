// Package domain holds the value objects for the universal-search bounded
// context (ADR-0024). Distinct from internal/discovery/domain — this context
// models a grouped LIBRARY-scope search result across four entities.
package domain

import (
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CollectionID is the surrogate PK of the collections table. New ID space —
// not shared with SeriesID/MovieID (primitive-obsession defense).
type CollectionID int64

// PersonID is the surrogate PK of the people table.
type PersonID int64

// SeriesHit is one library series match. TMDBID/Year/poster/backdrop are
// optional (unenriched rows carry NULLs).
type SeriesHit struct {
	SeriesID     shareddomain.SeriesID
	TMDBID       *shareddomain.TMDBID
	Title        string
	Year         *int
	PosterPath   *string
	BackdropPath *string
}

// MovieHit is one library movie match.
type MovieHit struct {
	MovieID      shareddomain.MovieID
	TMDBID       *shareddomain.TMDBID
	Title        string
	Year         *int
	PosterPath   *string
	BackdropPath *string
}

// CollectionHit is one library collection match. TMDBID carries the
// tmdb_collection_id (external). Collections have no popularity column.
type CollectionHit struct {
	CollectionID CollectionID
	TMDBID       *shareddomain.TMDBID
	Name         string
	PosterPath   *string
	BackdropPath *string
}

// PersonHit is one library-restricted person match (D7). KnownFor is the
// known_for_department string.
type PersonHit struct {
	PersonID    PersonID
	TMDBID      *shareddomain.TMDBID
	Name        string
	ProfilePath *string
	KnownFor    *string
}

// LibrarySearchResult is the grouped aggregate returned by the use case.
// Each group is independently ranked and capped at limitPerGroup. Groups
// with no matches are left nil (JSON-friendly empty state).
//
// S1.2a populates Series + Movies; Collections + People are filled by S1.2b.
type LibrarySearchResult struct {
	Series      []SeriesHit
	Movies      []MovieHit
	Collections []CollectionHit
	People      []PersonHit
}

// IsEmpty reports whether every group is empty.
func (r LibrarySearchResult) IsEmpty() bool {
	return len(r.Series) == 0 && len(r.Movies) == 0 &&
		len(r.Collections) == 0 && len(r.People) == 0
}
