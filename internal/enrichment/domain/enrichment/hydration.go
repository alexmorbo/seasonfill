package enrichment

// HydrationLevel mirrors the hydration discriminator used by
// `domain/series.Hydration` and `domain/people.Hydration`. Kept
// as a string-typed enum in domain/enrichment so the transition
// rule (CanTransition) lives once and the package stays free of
// cross-domain imports — both series and people declare their
// own typed Hydration enum with identical "stub"/"full" values
// and string-convert at the wrapper boundary.
type HydrationLevel string

const (
	LevelStub HydrationLevel = "stub"
	LevelFull HydrationLevel = "full"
)

// CanTransition reports whether moving from one hydration level
// to another is legal. Rules:
//
//	stub → stub : legal (no-op re-write).
//	stub → full : legal (enrichment path — TMDB worker landed).
//	full → stub : ILLEGAL (would only happen via a bug — a worker
//	              must never downgrade an already-enriched row).
//	full → full : legal (re-enrichment / refresh).
//
// Empty-string `from` or `to` is normalised to LevelStub
// (defensive default — legacy rows / mis-initialised structs).
// Unknown levels (anything except "stub" / "full" / "") are
// REJECTED — surfaces accidental typos as transition failures
// instead of silent passes.
func CanTransition(from, to HydrationLevel) bool {
	from = normaliseLevel(from)
	to = normaliseLevel(to)
	if from == "" || to == "" {
		return false
	}
	// stub → full / stub → stub / full → full all legal.
	// full → stub is the only illegal transition.
	return from != LevelFull || to != LevelStub
}

func normaliseLevel(l HydrationLevel) HydrationLevel {
	switch l {
	case "":
		return LevelStub
	case LevelStub, LevelFull:
		return l
	}
	return ""
}
