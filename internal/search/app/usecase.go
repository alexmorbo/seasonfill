package app

import (
	"context"
	"fmt"
	"strings"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
)

// defaultLimitPerGroup caps each entity group when the caller passes <= 0.
const defaultLimitPerGroup = 20

// UnifiedSearchUseCase orchestrates library-scope search across the entity
// groups. Construct via NewUnifiedSearchUseCase. Stateless / concurrency-safe.
type UnifiedSearchUseCase struct {
	repo LibrarySearchRepository
}

// NewUnifiedSearchUseCase binds the use case to a repository. repo MUST be
// non-nil — panics at wiring time so a boot bug surfaces immediately.
func NewUnifiedSearchUseCase(repo LibrarySearchRepository) *UnifiedSearchUseCase {
	if repo == nil {
		panic("unified search use case: repository required")
	}
	return &UnifiedSearchUseCase{repo: repo}
}

// SearchLibrary runs the grouped local search. q is trimmed; an empty or
// whitespace-only query short-circuits to an empty result with no error.
// limitPerGroup <= 0 defaults to 20.
//
// v1 queries the groups sequentially — correctness-first, and the per-group
// queries are index-assisted on Postgres. Concurrent fan-out (errgroup) is a
// possible later optimization; kept simple here.
func (uc *UnifiedSearchUseCase) SearchLibrary(ctx context.Context, q, language string, limitPerGroup int) (searchdomain.LibrarySearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return searchdomain.LibrarySearchResult{}, nil
	}
	if limitPerGroup <= 0 {
		limitPerGroup = defaultLimitPerGroup
	}

	var out searchdomain.LibrarySearchResult

	series, err := uc.repo.SearchSeries(ctx, q, language, limitPerGroup)
	if err != nil {
		return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library series: %w", err)
	}
	out.Series = series

	movies, err := uc.repo.SearchMovies(ctx, q, language, limitPerGroup)
	if err != nil {
		return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library movies: %w", err)
	}
	out.Movies = movies

	collections, err := uc.repo.SearchCollections(ctx, q, language, limitPerGroup)
	if err != nil {
		return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library collections: %w", err)
	}
	out.Collections = collections

	people, err := uc.repo.SearchPeople(ctx, q, language, limitPerGroup)
	if err != nil {
		return searchdomain.LibrarySearchResult{}, fmt.Errorf("search library people: %w", err)
	}
	out.People = people

	return out, nil
}
