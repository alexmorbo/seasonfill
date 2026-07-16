// Package enrichment — Story 534.
//
// RefreshTTL declares the per-tier freshness windows used by the
// background refresh scheduler (internal/enrichment/app/refresh_scheduler.go).
// Distinct from enrichment.TTL (which gates the in-band Handle
// staleness check) because the background scheduler operates on a
// coarser "how often should we proactively recheck" cadence — not the
// "do we even need to refetch on this request" decision the worker
// already makes per call.
//
// PRD §5.5 cross-ref: TMDB TTL is 24h for the synchronous refresh path
// (degraded marker semantics). The background scheduler runs longer
// horizons because it is amortising load across the day, not
// servicing a user-visible read.
package enrichment

import "time"

// RefreshTier identifies one of the four refresh tiers. RefreshTierChanged
// (Wave 2 / TMDB /tv/changes) is a flag-driven tier — a series enters it
// when tmdb_changed_at marks it as changed, not because a TTL window
// elapsed — and is deliberately valued 0 so `ORDER BY tier ASC` drains it
// before the three Story 534 TTL tiers.
type RefreshTier int

const (
	// RefreshTierChanged — series that TMDB /tv/changes flagged as changed
	// (series.tmdb_changed_at set, enrichment_tmdb_synced_at not yet caught
	// up). Value 0 so it sorts FIRST in the tiered picker. Not a TTL tier:
	// RefreshTTL.For never returns a duration for it (the picker's tier-0
	// arm is gated on the changed-pending predicate, not a cutoff).
	RefreshTierChanged RefreshTier = 0
	// RefreshTierHot — series present in at least one Sonarr library
	// (series_cache row exists, deleted_at IS NULL). User cares most.
	RefreshTierHot RefreshTier = 1
	// RefreshTierNormal — series referenced by discovery_lists rows
	// (user-visible discovery rails). User likely to view soon.
	RefreshTierNormal RefreshTier = 2
	// RefreshTierCold — every other TMDB-enrichable series in DB
	// (legacy stubs, recommendations cache). Refresh, but rarely.
	RefreshTierCold RefreshTier = 3
)

// String returns the label used in metric tags and slog attrs. Must
// stay low-cardinality (4 values) — never include a series id here.
func (t RefreshTier) String() string {
	switch t {
	case RefreshTierChanged:
		return "changed"
	case RefreshTierHot:
		return "hot"
	case RefreshTierNormal:
		return "normal"
	case RefreshTierCold:
		return "cold"
	default:
		return "unknown"
	}
}

// RefreshTTL is the per-tier freshness window. A series is considered
// "stale" when enrichment_tmdb_synced_at IS NULL OR < now - TTL.
type RefreshTTL struct {
	Hot    time.Duration
	Normal time.Duration
	Cold   time.Duration
}

// DefaultRefreshTTL is the production schedule. Tuned for ~50 series
// per 30-min tick assuming a steady-state library of a few thousand:
//   - Hot 7d  → 95% library refreshed weekly on small libraries.
//   - Normal 14d → discovery rails see refreshes biweekly.
//   - Cold 30d → stubs hit once a month; floor against unbounded growth.
func DefaultRefreshTTL() RefreshTTL {
	return RefreshTTL{
		Hot:    7 * 24 * time.Hour,
		Normal: 14 * 24 * time.Hour,
		Cold:   30 * 24 * time.Hour,
	}
}

// For returns the per-tier duration; falls through to Cold when the
// tier is unrecognised so a misconfigured caller cannot accidentally
// schedule a 0-TTL ("refresh everything every tick") sweep.
// RefreshTierChanged has no TTL (the picker's tier-0 arm is gated on the
// changed-pending predicate, not a cutoff), so it intentionally has no
// case here — a caller must never pass it to For.
func (t RefreshTTL) For(tier RefreshTier) time.Duration {
	switch tier {
	case RefreshTierHot:
		return t.Hot
	case RefreshTierNormal:
		return t.Normal
	case RefreshTierCold:
		return t.Cold
	default:
		return t.Cold
	}
}
