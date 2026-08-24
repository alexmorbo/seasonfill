package app

import (
	"context"
	"fmt"
	"strings"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// defaultLimitPerGroup caps each entity group when the caller passes <= 0.
const defaultLimitPerGroup = 20

// Scope selects which layers a search reads (D10). The handler maps the
// scope= query string into this enum.
type Scope int

const (
	ScopeLibrary Scope = iota // local library only (S1.4 behavior)
	ScopeCatalog              // TMDB catalog only
	ScopeAll                  // library first, then deduped catalog (D8)
)

// UnifiedSearchUseCase orchestrates scope dispatch + library/catalog merge.
// Construct via NewUnifiedSearchUseCase. Stateless / concurrency-safe.
type UnifiedSearchUseCase struct {
	repo    LibrarySearchRepository
	catalog CatalogSearchRepository
}

// NewUnifiedSearchUseCase binds the use case to both repositories. BOTH are
// required — panics at wiring time so a boot bug surfaces immediately. (The
// catalog adapter itself tolerates a disabled TMDB client and returns empty;
// the repo reference is still mandatory.)
func NewUnifiedSearchUseCase(repo LibrarySearchRepository, catalog CatalogSearchRepository) *UnifiedSearchUseCase {
	switch {
	case repo == nil:
		panic("unified search use case: library repository required")
	case catalog == nil:
		panic("unified search use case: catalog repository required")
	}
	return &UnifiedSearchUseCase{repo: repo, catalog: catalog}
}

// Search dispatches on scope. q is trimmed; empty/whitespace short-circuits to
// an empty result (no error). limitPerGroup <= 0 defaults to 20. types gates
// which groups are queried.
//
//   - ScopeLibrary: library groups only (source=library). Byte-identical to
//     the S1.4 SearchLibrary path.
//   - ScopeCatalog: catalog groups only (source=catalog).
//   - ScopeAll:     library first, then catalog appended, deduped per-type by
//     tmdb_id (a title already in the library is not repeated in catalog).
func (uc *UnifiedSearchUseCase) Search(ctx context.Context, q, language string, limitPerGroup int, scope Scope, types TypeFilter) (searchdomain.LibrarySearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return searchdomain.LibrarySearchResult{}, nil
	}
	if limitPerGroup <= 0 {
		limitPerGroup = defaultLimitPerGroup
	}

	switch scope {
	case ScopeCatalog:
		return uc.catalog.SearchCatalog(ctx, q, language, limitPerGroup, types)
	case ScopeAll:
		lib, err := uc.searchLibrary(ctx, q, language, limitPerGroup, types)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, err
		}
		cat, err := uc.catalog.SearchCatalog(ctx, q, language, limitPerGroup, types)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, err
		}
		return mergeDedup(lib, cat), nil
	default: // ScopeLibrary
		return uc.searchLibrary(ctx, q, language, limitPerGroup, types)
	}
}

// SearchLibrary is the S1.4 back-compat entry (library-only, all types). Kept
// so existing callers/tests are unchanged; delegates to Search.
func (uc *UnifiedSearchUseCase) SearchLibrary(ctx context.Context, q, language string, limitPerGroup int) (searchdomain.LibrarySearchResult, error) {
	return uc.Search(ctx, q, language, limitPerGroup, ScopeLibrary, AllTypes())
}

// searchLibrary runs the per-group local queries, gated by types. Excluded
// groups are skipped (left empty). Error wrap strings match S1.4.
func (uc *UnifiedSearchUseCase) searchLibrary(ctx context.Context, q, language string, limit int, types TypeFilter) (searchdomain.LibrarySearchResult, error) {
	var out searchdomain.LibrarySearchResult

	if types.Series {
		series, err := uc.repo.SearchSeries(ctx, q, language, limit)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library series: %w", err)
		}
		out.Series = series
	}
	if types.Movie {
		movies, err := uc.repo.SearchMovies(ctx, q, language, limit)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library movies: %w", err)
		}
		out.Movies = movies
	}
	if types.Collection {
		collections, err := uc.repo.SearchCollections(ctx, q, language, limit)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library collections: %w", err)
		}
		out.Collections = collections
	}
	if types.Person {
		people, err := uc.repo.SearchPeople(ctx, q, language, limit)
		if err != nil {
			return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library people: %w", err)
		}
		out.People = people
	}
	return out, nil
}

// mergeDedup appends catalog hits after library hits (library-first, D8),
// dropping any catalog hit whose tmdb_id is already present in the SAME type's
// library group (per-type sets, decision (e)). Library hits with a nil tmdb_id
// never enter a set and are always kept.
func mergeDedup(lib, cat searchdomain.LibrarySearchResult) searchdomain.LibrarySearchResult {
	return searchdomain.LibrarySearchResult{
		Series: dedup(lib.Series, cat.Series, func(h searchdomain.SeriesHit) *shareddomain.TMDBID { return h.TMDBID }),
		Movies: dedup(lib.Movies, cat.Movies, func(h searchdomain.MovieHit) *shareddomain.TMDBID { return h.TMDBID }),
		Collections: dedup(lib.Collections, cat.Collections,
			func(h searchdomain.CollectionHit) *shareddomain.TMDBID { return h.TMDBID }),
		People: dedup(lib.People, cat.People, func(h searchdomain.PersonHit) *shareddomain.TMDBID { return h.TMDBID }),
	}
}

// dedup returns libHits followed by the catHits whose tmdb_id is not already in
// libHits. A fresh set per call keeps the four types independent (tmdb id
// spaces overlap across media types).
func dedup[T any](libHits, catHits []T, tmdbOf func(T) *shareddomain.TMDBID) []T {
	seen := make(map[shareddomain.TMDBID]struct{}, len(libHits))
	for _, h := range libHits {
		if id := tmdbOf(h); id != nil {
			seen[*id] = struct{}{}
		}
	}
	out := make([]T, 0, len(libHits)+len(catHits))
	out = append(out, libHits...)
	for _, h := range catHits {
		if id := tmdbOf(h); id != nil {
			if _, dup := seen[*id]; dup {
				continue
			}
		}
		out = append(out, h)
	}
	return out
}
