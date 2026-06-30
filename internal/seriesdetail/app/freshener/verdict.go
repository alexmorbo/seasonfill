package freshener

import "time"

// SectionVerdict — per-section freshness assessment from Probe.
//
// Stale=true means the consumer (A5 EnsureFreshScope) should dispatch
// the corresponding narrow Worker method. Stale=false means the section
// data on disk satisfies the TTL policy for the requested lang.
//
// Reason is a short opaque label used in logs + metrics; downstream
// code never branches on it (the boolean is the contract). Allowed
// values:
//
//	"never"        — synced_at column NULL (cold load)
//	"expired"      — age > TTLPolicy.Ceiling
//	"status"       — show is Returning Series/In Production AND age > Floor
//	"missing_lang" — series_texts row absent for requested lang
//	"missing_episodes_lang" — episode_texts coverage < threshold (Story 548 fail-safe)
//	"stub"         — Canon hydration != HydrationFull (no canon → no freshness)
//	"probe_error"  — IO error reading a synced_at column (fail-open per Radarr lesson)
//	"fresh"        — Stale=false
type SectionVerdict struct {
	Section  Section
	Stale    bool
	Reason   string
	SyncedAt *time.Time    // nil if never synced
	Age      time.Duration // 0 if SyncedAt is nil
}
