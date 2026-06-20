// Package rest is the (reserved) interface leaf of the discovery
// bounded context. It will host the Gin handlers for the global
// discovery surface (see PRD §5.1 N-2 endpoint table):
//
//   - GET /api/v1/discovery/trending
//   - GET /api/v1/discovery/popular
//   - GET /api/v1/discovery/genre/{genre_id}
//   - GET /api/v1/discovery/network/{network_id}
//   - GET /api/v1/discovery/keyword/{keyword_id}
//   - GET /api/v1/discovery/search?q={query}&lang={lang}
//   - GET /api/v1/discovery/genres
//   - GET /api/v1/discovery/networks
//   - GET /api/v1/discovery/discover
//
// Status: SKELETON (story 447 A-1-21). The package is intentionally
// empty during Phase 1 — the directory exists so the internal/
// tree matches PRD §3.2 ahead of Phase 3 N-2 shipping the feature.
//
// Import direction (PRD §3.3): once populated, rest MAY import
// internal/discovery/app (use-case wiring), internal/discovery/domain
// (value types), and the shared HTTP kernel (interface/http/dto until
// it relocates into internal/shared/dto/). It MUST NOT reach into
// internal/discovery/persistence, and MUST NOT depend on other
// bounded contexts' rest leaves. The depcheck guard
// tests/lint_discovery_imports_test.go pins this rule from
// story 447 onward.
package rest
