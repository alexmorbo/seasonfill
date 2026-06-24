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
	// HasAnyList reports whether ANY discovery_lists row exists. Used by
	// the worker's post-Tick warming probe: a redeploy against an already-
	// populated DB takes the "fresh, skip refresh" branch through every
	// (kind, lang) pair, leaving warmingOnce=false forever. HasAnyList lets
	// the worker flip warming via a single cheap probe so handlers stop
	// emitting the cold-start envelope after the first Tick.
	HasAnyList(ctx context.Context) (bool, error)
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

// WarmingProbe is the narrow read-only surface story 507 handlers use
// to render a cold-start envelope on /discovery/trending and /popular
// before the worker's first successful list refresh. Satisfied by
// *Worker.IsWarming.
type WarmingProbe interface {
	IsWarming() bool
}

// RefreshOnDemand is the narrow write surface story 507 long-tail
// handlers (/discovery/genre /network /keyword) use to trigger an
// inline refresh when the requested (kind, param, lang) tuple is
// missing or stale-by-7d. Satisfied by *Worker.RefreshNow.
//
// Concurrency note (mirrors Worker.RefreshNow godoc): callers MUST
// de-dupe at the (kind, param, lang) key — the worker does NOT
// coalesce concurrent invocations. Story 507's HTTP handlers use
// singleflight to collapse parallel cold-cache requests onto one
// TMDB fetch.
type RefreshOnDemand interface {
	RefreshNow(ctx context.Context, kind disco.Kind, param, lang string) error
}

// LibraryInstancesPort is the narrow read-only contract that handlers
// use to surface DiscoverySeriesItem.InLibraryInstances. The discovery
// projection runs over the canon series_id keyspace; the implementation
// fans those ids out into one batched series_cache lookup per response.
//
// Contract:
//   - Input: a (possibly empty) slice of canonical series ids.
//   - Output: map keyed on the input id, value = sorted distinct
//     non-empty instance name slice, soft-deleted rows excluded.
//   - Missing entries: an input id with zero active cache rows MAY
//     be omitted from the result OR mapped to []string{}. Both
//     shapes encode the same UX intent ("not in any library").
//   - Empty input slice: implementations MUST short-circuit before
//     issuing any SQL and return an empty (non-nil) map.
//   - Concurrency: callers MAY invoke this concurrently — discovery
//     handlers serve goroutines per request.
//
// Implementation: wiring/discovery.go libraryInstancesAdapter bridges
// catalog SeriesCacheRepository.GetInstancesBySeriesIDs into this port.
// Discovery never imports catalog directly (PRD §3.3 vertical slice).
type LibraryInstancesPort interface {
	ListByCanonicalSeriesIDs(ctx context.Context, ids []shareddomain.SeriesID) (map[shareddomain.SeriesID][]string, error)
}
