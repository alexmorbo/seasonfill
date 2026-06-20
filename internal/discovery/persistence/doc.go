// Package persistence is the (reserved) persistence leaf of the
// discovery bounded context. It will host the Postgres repository
// for the discovery_lists table (kind, param, language, position,
// refreshed_at) — the durable backing for trending / popular /
// by_genre / by_network / by_keyword ranked lists per active
// preferred_language (see PRD §4 D-1 greenfield schema + §5.1.1).
//
// Status: SKELETON (story 447 A-1-21). The package is intentionally
// empty during Phase 1 — the directory exists so the internal/
// tree matches PRD §3.2 ahead of Phase 3 N-2 shipping the feature
// and the D-1 greenfield migration introducing the discovery_lists
// table.
//
// Import direction (PRD §3.3): once populated, persistence MAY
// import internal/discovery/domain and the kernel persistence
// surfaces (internal/shared/database, GORM) — but MUST NOT reach
// into internal/discovery/app or internal/discovery/rest, and MUST
// NOT cross into other bounded contexts' persistence leaves. The
// depcheck guard tests/lint_discovery_imports_test.go pins this
// rule from story 447 onward.
package persistence
