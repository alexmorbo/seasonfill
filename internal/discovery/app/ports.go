// ports.go declares the discovery bounded-context application surface:
// ports the worker (story 506) and handler (story 507) read through.
// The actual worker + HTTP wirers land in those follow-up stories;
// this file only declares the contracts so story 505's persistence
// implementation has an interface to satisfy.
//
// Import rule (PRD §3.3): app imports internal/discovery/domain +
// stdlib + internal/shared/* only. Pinned by
// tests/lint_discovery_imports_test.go.
package app

import (
	"context"
	"time"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// DiscoveryListRepo is the persistence contract for the discovery_lists
// table. Implementation: internal/discovery/persistence.ListRepository
// (story 505 Commit B). Consumers: story 506 worker + story 507
// handler.
//
// PRD §5.1.1: page is 1-indexed; perPage is the response slice length.
// kind + param + language form the lookup key; param is the empty
// string for kind=trending_day / trending_week / popular and the
// genre / network / keyword id for the by_* variants.
type DiscoveryListRepo interface {
	GetRanked(ctx context.Context, kind disco.Kind, param, language string, page, perPage int) (disco.Page, error)
	IsStale(ctx context.Context, kind disco.Kind, param, language string, ttl time.Duration) (bool, error)
	LastRefreshedAt(ctx context.Context, kind disco.Kind, param, language string) (time.Time, error)
	ReplaceList(ctx context.Context, kind disco.Kind, param, language string, items []disco.Item) error
}

// StubUpserter is the narrow port discovery uses to materialise an
// unknown TMDB series into the local series table — the worker calls
// EnsureStub when a Trending / Popular / Discover response surfaces a
// tmdb_id without a matching local row (PRD §5.1.1 stub-upsert
// invariant). Implementation: a `stubUpserterAdapter` in
// internal/wiring/discovery.go wraps
// internal/enrichment/persistence.SeriesRepository.UpsertStub — the
// adapter lives in wiring so discovery never imports enrichment.
type StubUpserter interface {
	EnsureStub(ctx context.Context, tmdbID shareddomain.TMDBID, title string, poster, backdrop *string) (shareddomain.SeriesID, error)
}

// ActiveLanguagesProvider returns the set of preferred_language values
// the discovery worker should refresh against. Implementation:
// internal/discovery/persistence.ActiveLanguagesRepository.
//
// Contract (PRD §5.1.1 line 551, rewritten for the D-1 `users` schema):
// every distinct non-empty users.preferred_language, UNIONed with
// "en-US". Sorted ascending for deterministic iteration order in the
// worker (story 506 enqueues per language in a stable order).
type ActiveLanguagesProvider interface {
	ActiveLanguages(ctx context.Context) ([]string, error)
}
