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
	// RefreshTierChanged — TMDB /tv/changes flagged. Value 0 → drains first.
	RefreshTierChanged RefreshTier = 0
	// RefreshTierHot — present in ≥1 Sonarr library (series_cache row live).
	RefreshTierHot RefreshTier = 1
	// RefreshTierFollowed — on the follow/watchlist (followed_series row) but
	// NOT in any library. Sits between Hot and Normal so a followed-not-in-
	// library series keeps a tight refresh cadence (F-04: else its air_date
	// decays to Cold-TTL and the calendar rots).
	RefreshTierFollowed RefreshTier = 2
	// RefreshTierNormal — referenced by discovery_lists (user-visible rails).
	RefreshTierNormal RefreshTier = 3
	// RefreshTierCold — every other TMDB-enrichable series. Refresh, but rarely.
	RefreshTierCold RefreshTier = 4
)

// String returns the low-cardinality metric/slog label (5 values now).
func (t RefreshTier) String() string {
	switch t {
	case RefreshTierChanged:
		return "changed"
	case RefreshTierHot:
		return "hot"
	case RefreshTierFollowed:
		return "followed"
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
	Hot      time.Duration
	Followed time.Duration
	Normal   time.Duration
	Cold     time.Duration
}

// DefaultRefreshTTL is the production schedule.
//   - Hot 7d, Followed 10d (tighter than Normal — the user explicitly asked
//     to track these), Normal 14d, Cold 30d.
func DefaultRefreshTTL() RefreshTTL {
	return RefreshTTL{
		Hot:      7 * 24 * time.Hour,
		Followed: 10 * 24 * time.Hour,
		Normal:   14 * 24 * time.Hour,
		Cold:     30 * 24 * time.Hour,
	}
}

// For returns the per-tier duration; unknown/Changed fall through to Cold so a
// misconfigured caller cannot schedule a 0-TTL sweep. (Changed has no TTL — the
// picker gates it on the changed-pending predicate, not a cutoff.)
func (t RefreshTTL) For(tier RefreshTier) time.Duration {
	switch tier {
	case RefreshTierHot:
		return t.Hot
	case RefreshTierFollowed:
		return t.Followed
	case RefreshTierNormal:
		return t.Normal
	case RefreshTierCold:
		return t.Cold
	default:
		return t.Cold
	}
}
