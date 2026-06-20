// Package app is the (reserved) application leaf of the discovery
// bounded context. It will host DiscoveryWorker — the background
// loop that refreshes discovery_lists against TMDB
// Trending/Popular/Discover/Search per active preferred_language —
// alongside the on-demand stale-list refresh strategy and the
// search fallback chain (local LIKE → TMDB) described in
// PRD §5.1.1 / §5.1.2.
//
// Status: SKELETON (story 447 A-1-21). The package is intentionally
// empty during Phase 1 — the directory exists so the internal/
// tree matches PRD §3.2 ahead of Phase 3 N-2 shipping the feature.
//
// Import direction (PRD §3.3): once populated, app MAY import
// internal/discovery/domain and the kernel surfaces
// (internal/shared/clients/tmdb, internal/shared/ports), but MUST
// NOT reach into internal/discovery/persistence or
// internal/discovery/rest for behaviour. Cross-context behaviour
// reaches in via narrow ports.go contracts (Enrichment.EnsureStub
// for stub-upserting unknown TMDB series). The depcheck guard
// tests/lint_discovery_imports_test.go pins this rule from
// story 447 onward.
package app
