// schedule.go declares the per-kind refresh cadence + per-kind page
// budget used by the DiscoveryWorker (Tick + refresh). Pure functions
// against the disco.Kind enum — no DB / TMDB dependency, so the
// worker_test consumes the same source of truth as production.
//
// Cadence rationale (PRD §5.1.1 lines 623-710):
//   - trending_day: refresh every 6h (fast-moving leaderboard)
//   - trending_week / popular: every 24h (slow-moving leaderboard)
//   - by_genre / by_network: every 24h (catalog-derived; worker
//     auto-iterates the top-10 of each via persistence.TopKindsReader)
//   - by_keyword: every 7d (rarely surfaced via worker; the handler
//     story 507 covers the on-demand refresh path for keyword lists)
//
// Page budget: trending + popular take 5 pages × 20 = 100 items; the
// curated by_* lists take 3 pages = 60 items (PRD §5.1.1 line 645).
//
// Import rule: app imports internal/discovery/domain + stdlib only.
// Pinned by tests/lint_discovery_imports_test.go.
package app

import (
	"time"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// ScheduleFor returns the refresh-interval for a list kind. The
// worker queries repo.IsStale(kind, …, ScheduleFor(kind)) on every
// 1h tick — when the repo answer is true (i.e. max(refreshed_at)
// is older than the returned interval, or the list has no rows yet)
// the worker pulls a fresh page from TMDB.
//
// Unknown kind → 24h (defensive: a future Kind constant that lands
// in the domain enum but is forgotten here still gets a reasonable
// cadence rather than refreshing on every 1h tick).
func ScheduleFor(kind disco.Kind) time.Duration {
	switch kind {
	case disco.KindTrendingDay:
		return 6 * time.Hour
	case disco.KindTrendingWeek, disco.KindPopular,
		disco.KindByGenre, disco.KindByNetwork:
		return 24 * time.Hour
	case disco.KindByKeyword:
		return 7 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// PagesFor returns the page budget per refresh — the worker fetches
// 1..PagesFor(kind) sequentially from TMDB, stitches the per-page
// results[] together, and emits a single repo.ReplaceList. 5 pages
// × 20 entries = 100 items for the leaderboards; 3 pages = 60 items
// for the curated by_* lists.
//
// Unknown kind → 1 page (defensive: same rationale as ScheduleFor).
func PagesFor(kind disco.Kind) int {
	switch kind {
	case disco.KindTrendingDay, disco.KindTrendingWeek, disco.KindPopular:
		return 5
	case disco.KindByGenre, disco.KindByNetwork, disco.KindByKeyword:
		return 3
	default:
		return 1
	}
}
