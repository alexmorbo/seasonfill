package wiring

import (
	"log/slog"

	"gorm.io/gorm"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchcatalog "github.com/alexmorbo/seasonfill/internal/search/catalog"
	searchpersistence "github.com/alexmorbo/seasonfill/internal/search/persistence"
	searchrest "github.com/alexmorbo/seasonfill/internal/search/rest"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// BuildSearch wires GET /api/v1/search (ADR-0024 S1.3): library repo +
// catalog adapter → UnifiedSearchUseCase → SearchHandler. catalogClient is the
// live runtime TMDB surface (nil-OK — a nil client means TMDB is disabled at
// boot, and the catalog adapter degrades every catalog group to empty). Reuses
// the existing "search" DomainLogger domain — NO new domain (S1.4 CI-red trap).
func BuildSearch(db *gorm.DB, catalogClient searchcatalog.TMDBSearchClient, base *slog.Logger) *searchrest.SearchHandler {
	log := sharedports.DomainLogger(base, "search")
	repo := searchpersistence.NewLibrarySearchRepository(db)
	catalogRepo := searchcatalog.NewAdapter(catalogClient, log)
	uc := searchapp.NewUnifiedSearchUseCase(repo, catalogRepo)
	return searchrest.NewSearchHandler(uc, log)
}
