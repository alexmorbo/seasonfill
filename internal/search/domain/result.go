// Package domain holds the value objects for the universal-search bounded
// context (ADR-0024). Distinct from internal/discovery/domain — this context
// models a grouped search result across four entities. Hits carry a Source
// discriminator ("library" | "catalog", S1.3) so the projection can tag
// provenance without a second type hierarchy.
package domain

import (
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Source discriminator values. Defined here (not in rest/dto) because BOTH
// writer layers stamp them: persistence (library) and the catalog adapter
// (catalog). Single DRY home; the dto layer maps these → its own wire consts.
const (
	SourceLibrary = "library"
	SourceCatalog = "catalog"
)

// CollectionID is the surrogate PK of the collections table (0 for catalog
// hits — they are not in the library).
type CollectionID int64

// PersonID is the surrogate PK of the people table.
type PersonID int64

// SeriesHit is one series match. SeriesID is 0 for catalog hits.
type SeriesHit struct {
	SeriesID     shareddomain.SeriesID
	TMDBID       *shareddomain.TMDBID
	Title        string
	Year         *int
	PosterPath   *string
	BackdropPath *string
	Source       string
}

// MovieHit is one movie match.
type MovieHit struct {
	MovieID      shareddomain.MovieID
	TMDBID       *shareddomain.TMDBID
	Title        string
	Year         *int
	PosterPath   *string
	BackdropPath *string
	Source       string
}

// CollectionHit is one collection match.
type CollectionHit struct {
	CollectionID CollectionID
	TMDBID       *shareddomain.TMDBID
	Name         string
	PosterPath   *string
	BackdropPath *string
	Source       string
}

// PersonHit is one person match. KnownFor is the known_for_department string.
type PersonHit struct {
	PersonID    PersonID
	TMDBID      *shareddomain.TMDBID
	Name        string
	ProfilePath *string
	KnownFor    *string
	Source      string
}

// LibrarySearchResult is the grouped aggregate returned by the use case and by
// the catalog adapter. Each group is independently ranked and capped. The name
// is historical (S1.2a) — it now carries either library- or catalog-sourced
// hits, distinguished per hit by Source; kept stable to avoid churning
// S1.2a/S1.4.
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
