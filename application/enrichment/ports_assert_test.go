package enrichment_test

import (
	"github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// Compile-time guarantee: *tmdb.Client satisfies the TMDBClient
// port. If the interface or the production implementation drifts
// apart, the build breaks here, NOT at C-2's worker code.
var _ enrichment.TMDBClient = (*tmdb.Client)(nil)
