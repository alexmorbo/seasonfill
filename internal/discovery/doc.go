// Package discovery is the bounded-context placeholder that will own
// the seasonfill global discovery surface: Seerr-style search,
// trending, popular, by-genre, by-network, by-keyword, recommendations,
// and similar listings — none of which are tied to a single Sonarr
// instance (see PRD §3.1 G-2 + §5.1 N-2).
//
// Status: SKELETON. Phase 1 story 447 (A-1-21) carves out the
// vertical-slice directory tree per umbrella Q4 so the
// internal/ layout matches PRD §3.2 before Phase 3 actually ships
// the feature. Every leaf below currently contains only a doc.go
// reservation stub:
//
//	internal/discovery/
//	  domain/      — (reserved) discovery_lists value types, ranked
//	                 list kind enum (trending_day / popular / by_genre /
//	                 by_network / by_keyword), refresh-policy
//	                 invariants. Filled in Phase 3 N-2.
//	  app/         — (reserved) DiscoveryWorker background loop that
//	                 refreshes discovery_lists against TMDB Trending /
//	                 Popular / Discover / Search per active
//	                 preferred_language; on-demand refresh-on-stale for
//	                 genre/network/keyword lists; search fallback
//	                 (local LIKE → TMDB). Wired by
//	                 internal/wiring/discovery.go (placeholder added by
//	                 story 452 A-2). Filled in Phase 3 N-2.
//	  persistence/ — (reserved) discovery_lists Postgres repository
//	                 (kind, param, language, position, refreshed_at).
//	                 Schema lands with PRD §4 D-1 greenfield wave.
//	                 Filled in Phase 3 N-2.
//	  rest/        — (reserved) HTTP handlers for the discovery surface:
//	                   - GET /api/v1/discovery/trending
//	                   - GET /api/v1/discovery/popular
//	                   - GET /api/v1/discovery/genre/{genre_id}
//	                   - GET /api/v1/discovery/network/{network_id}
//	                   - GET /api/v1/discovery/keyword/{keyword_id}
//	                   - GET /api/v1/discovery/search?q={query}&lang={lang}
//	                   - GET /api/v1/discovery/genres
//	                   - GET /api/v1/discovery/networks
//	                   - GET /api/v1/discovery/discover
//	                 Filled in Phase 3 N-2.
//
// Import direction (PRD §3.3 — enforced by
// tests/lint_discovery_imports_test.go from this story onward): every
// package under internal/discovery/ MUST NOT import the horizontal-CA
// layers (application/, domain/, infrastructure/, interface/) at all.
// While the tree is empty there is nothing to import; the depcheck
// guard pins that invariant so a casual edit cannot accidentally
// breach the vertical-slice boundary before Phase 3 deliberately
// adds the carve-outs it actually needs.
//
// Cross-context boundary (forward-looking, no code yet):
//
//   - Enrichment.EnsureStub for stub-upserting series whose TMDB ID
//     surfaces in trending/popular/discover responses without already
//     existing in the local catalog (§3.1 N-2).
//   - shared/clients/tmdb for Trending / Popular / Discover / Search
//     calls (already-existing kernel client, no new infra).
//   - shared/ports.DomainLogger via internal/logger for the
//     domain="discovery" log channel (story F-4b reservation).
//
// Story origin:
//   - 447 — vertical-slice skeleton (this reservation).
//   - 452 — placeholder wiring (internal/wiring/discovery.go).
//   - Phase 3 N-2 — actual feature implementation.
package discovery
