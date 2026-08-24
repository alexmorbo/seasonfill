// Package app holds the universal-search use case and its ports (ADR-0024
// S1.2). The dependency rule: app defines the port, persistence implements
// it. Distinct names from internal/discovery/app (F-11).
package app

import (
	"context"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
)

// LibrarySearchRepository is the read port for library-scope search. Each
// method runs a dialect-branched query (Postgres uses the 000067 GIN trgm
// indexes; SQLite falls back to plain LOWER LIKE). Implemented by
// persistence.LibrarySearchRepository.
//
// S1.2b extends this interface with SearchCollections and SearchPeople.
type LibrarySearchRepository interface {
	// SearchSeries matches series_texts.title across all languages. limit
	// caps the group. Empty/whitespace q returns ([], nil).
	SearchSeries(ctx context.Context, q, language string, limit int) ([]searchdomain.SeriesHit, error)

	// SearchMovies matches movies.title ∪ movies.original_title ∪
	// movie_i18n.title (additive, F-03).
	SearchMovies(ctx context.Context, q, language string, limit int) ([]searchdomain.MovieHit, error)
}
