package series

import "github.com/alexmorbo/seasonfill/domain/enrichment"

// CanTransition reports whether moving a Canon's Hydration from
// `from` to `to` is legal. Thin typed wrapper around
// enrichment.CanTransition — the transition rule itself lives in
// domain/enrichment so series and people share one source of
// truth (PRD §5.3 — both Hydration enums have identical
// "stub"/"full" values; the invariant is single-rooted).
//
// Repository Upsert paths in series_repository.go call this
// before persisting a stub-on-full overwrite to defend against
// worker bugs; the SQL UPDATE proceeds only when this returns
// true.
func CanTransition(from, to Hydration) bool {
	return enrichment.CanTransition(
		enrichment.HydrationLevel(from),
		enrichment.HydrationLevel(to),
	)
}
