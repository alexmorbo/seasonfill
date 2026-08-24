package app

import (
	"context"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
)

// TypeFilter records which entity groups a search should populate. The handler
// maps its CSV types= filter into this; the use case gates both library and
// catalog queries on it (an excluded group skips its DB/TMDB call — saves work
// and, for catalog, TMDB quota). The zero value populates NOTHING; callers use
// AllTypes() for "no filter".
type TypeFilter struct {
	Series     bool
	Movie      bool
	Collection bool
	Person     bool
}

// AllTypes returns the "no filter" TypeFilter (all four groups).
func AllTypes() TypeFilter {
	return TypeFilter{Series: true, Movie: true, Collection: true, Person: true}
}

// CatalogSearchRepository is the read port for catalog-scope (TMDB) search.
// One grouped call owns the concurrent fan-out + per-group error isolation
// (decision (c)). Implemented by internal/search/catalog.Adapter.
//
// Contract: a failure in ONE TMDB group degrades that group to empty (logged
// WARN) and MUST NOT fail the whole call — SearchCatalog returns a nil error
// for per-group TMDB failures. A non-nil error is reserved for context
// cancellation. Every returned hit carries Source == searchdomain.SourceCatalog
// and a nil/zero internal library ID (catalog hits are not in the library).
type CatalogSearchRepository interface {
	SearchCatalog(ctx context.Context, q, language string, limit int, types TypeFilter) (searchdomain.LibrarySearchResult, error)
}
