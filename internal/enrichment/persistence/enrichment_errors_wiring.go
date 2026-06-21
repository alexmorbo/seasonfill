package persistence

import (
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
)

// Compile-time guard: the production EnrichmentErrorsRepository
// satisfies the app-layer EnrichmentErrorRepo port. The check lives
// here (next to the repo) so a method-signature drift surfaces in the
// package that owns the type, not in main.go's wiring. 464b consumes
// the repo via this port — the assertion stays through 464b/464c.
var _ appenrich.EnrichmentErrorRepo = (*EnrichmentErrorsRepository)(nil)
