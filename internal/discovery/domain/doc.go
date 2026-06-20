// Package domain is the (reserved) domain leaf of the discovery
// bounded context. It will host the discovery_lists value types,
// the ranked-list kind enum (trending_day / popular / by_genre /
// by_network / by_keyword), and the refresh-policy invariants that
// DiscoveryWorker depends on (see PRD §5.1 N-2 + §5.1.1).
//
// Status: SKELETON (story 447 A-1-21). The package is intentionally
// empty during Phase 1 — the directory exists so the internal/
// tree matches PRD §3.2 ahead of Phase 3 N-2 shipping the feature.
//
// Import direction (PRD §3.3): once populated, domain MUST NOT
// import application/, infrastructure/, interface/, or any other
// internal/discovery/ sub-leaf. Only stdlib + internal/shared/domain
// (kernel value types like SeriesID) are permitted. The depcheck
// guard tests/lint_discovery_imports_test.go pins this rule from
// story 447 onward.
package domain
