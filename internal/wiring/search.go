package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchpersistence "github.com/alexmorbo/seasonfill/internal/search/persistence"
	searchrest "github.com/alexmorbo/seasonfill/internal/search/rest"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// search.go wires the universal-search bounded context (ADR-0024 S1.4):
// persistence repo → UnifiedSearchUseCase → SearchHandler. The whole slice
// depends only on the app *gorm.DB (dialect-branched inside the repo), so it
// is always built — there is no TMDB dependency to gate on (the catalog layer
// lands in S1.3). db MUST be non-nil.

// BuildSearch wires GET /api/v1/search. base is the root logger — this
// tags it with the "search" domain, mirroring BuildDiscoveryRowConfig.
func BuildSearch(db *gorm.DB, base *slog.Logger) *searchrest.SearchHandler {
	log := sharedports.DomainLogger(base, "search")
	repo := searchpersistence.NewLibrarySearchRepository(db)
	uc := searchapp.NewUnifiedSearchUseCase(repo)
	return searchrest.NewSearchHandler(uc, log)
}
