// Package values holds the typed value-object kit introduced in E-1
// (PRD §4.3, Story 557 / E-1-A0). Each primitive used by the E-1
// SkeletonDTO (§7.1) is wrapped in a struct-based value object with a
// private field and a factory function — Object Calisthenics rule #3
// "Wrap All Primitives And Strings". Construction is the only entry
// point that performs validation, so an instance that exists is
// always valid.
//
// Scope policy:
//   - NEW E-1 DTO types (Phase 2+): use these VOs.
//   - Existing A-5 IDs in internal/shared/domain/ids.go (SeriesID,
//     EpisodeID, TMDBID, …): stay as named primitives. Heterogeneity is
//     accepted — retrofitting them is a future E-2 epic.
//   - The AST guard tests/lint_no_bare_primitives_test.go fires only on
//     NEW Phase 2+ code where a VO equivalent exists.
package values
