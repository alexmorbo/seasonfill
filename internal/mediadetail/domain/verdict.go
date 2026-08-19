package domain

// SectionVerdict is one section's freshness assessment produced by a
// SectionPlugin's Staleness method. It is a pure value.
//
// Stale=true means the engine should Refresh that section. Stale=false means
// the section on disk satisfies the plugin's policy for the requested language.
//
// Reason is an opaque short label for logs/metrics only — downstream code never
// branches on it; THE BOOLEAN IS THE CONTRACT. This ports the boolean semantics
// of internal/seriesdetail/app/freshener.SectionVerdict but DROPS SyncedAt/Age
// (those were series-specific probe internals; the engine only needs the bool).
type SectionVerdict struct {
	Section Section
	Stale   bool
	Reason  string
}
