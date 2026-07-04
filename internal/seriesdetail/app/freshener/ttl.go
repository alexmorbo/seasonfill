package freshener

import "time"

// TTLPolicy describes the per-section state-machine thresholds inspired
// by Sonarr SeriesService.IsRefreshAllowed. Two thresholds:
//
//	Floor — minimum age below which we never refresh (debounce burst opens).
//	Ceiling — maximum age above which we ALWAYS refresh.
//
// Between Floor and Ceiling the verdict depends on series.status —
// "Returning Series" / "In Production" rows refresh sooner because
// upstream metadata changes more frequently. Ended/Canceled stay fresh
// until Ceiling fires.
//
// Currently series.status-aware logic is implemented for Sections that
// historically churn (Overview, Cast, Skeleton). Media/Recommendations
// use plain Floor/Ceiling — image hashes + recommendation lists are
// not driven by show status.
type TTLPolicy struct {
	Floor       time.Duration
	Ceiling     time.Duration
	StatusAware bool // true → in production/returning shows refresh at Floor; ended at Ceiling
}

// SectionTTLs is the canonical per-section policy table.
//
// Tuning rationale:
//
//   - Skeleton 24h floor / 7d ceiling: hero + season summaries change
//     rarely (poster swaps, next_air_date moves). Returning shows
//     refresh ~daily to catch air-date moves; ended at 7d.
//   - Overview 24h floor / 7d ceiling status-aware: same as skeleton —
//     overview text is canon-localised, low churn outside Returning.
//   - Cast 7d floor / 30d ceiling status-aware: cast turnover is rare
//     even on returning shows; ended shows almost never change.
//   - Recommendations 7d floor / 30d ceiling: TMDB recommendations
//     drift weekly with their popularity feed; not status-bound.
//   - Media 7d floor / 30d ceiling: image hashes stable once committed.
//   - SeasonSection 24h floor / 7d ceiling status-aware: per-season
//     episode list churn on Returning Series during airing windows.
//
// All durations are loose upper bounds; ChangesSyncer (Phase 4) will
// trigger force=true refreshes when TMDB actually signals a change,
// short-circuiting these passive TTLs.
var SectionTTLs = map[Section]TTLPolicy{
	SectionSkeleton:        {Floor: 24 * time.Hour, Ceiling: 7 * 24 * time.Hour, StatusAware: true},
	SectionOverview:        {Floor: 24 * time.Hour, Ceiling: 7 * 24 * time.Hour, StatusAware: true},
	SectionCast:            {Floor: 7 * 24 * time.Hour, Ceiling: 30 * 24 * time.Hour, StatusAware: true},
	SectionRecommendations: {Floor: 7 * 24 * time.Hour, Ceiling: 30 * 24 * time.Hour},
	SectionMedia:           {Floor: 7 * 24 * time.Hour, Ceiling: 30 * 24 * time.Hour},
}

// SeasonTTL is the policy applied to every SeasonSection verdict.
// Separate var (not in SectionTTLs) because the key is dynamic
// ("season:N") and we don't want to populate the map at lookup time.
var SeasonTTL = TTLPolicy{Floor: 24 * time.Hour, Ceiling: 7 * 24 * time.Hour, StatusAware: true}

// isReturning reports whether the canon series status warrants
// status-aware early refresh. Accepts BOTH vocabularies that can land
// in series.status: TMDB's canonical case ("Returning Series",
// "In Production") AND Sonarr's coarse lowercase ("continuing"), which
// sonarr_sync writes as a fallback for tmdb-less rows. Sonarr's
// "ended"/"deleted" correctly fall through to false, matching TMDB's
// "Ended"/"Canceled".
func isReturning(status *string) bool {
	if status == nil {
		return false
	}
	switch *status {
	case "Returning Series", "In Production", "continuing":
		return true
	default:
		return false
	}
}

// ttlVerdict applies a TTLPolicy to (syncedAt, status, now) → (stale, reason).
// Pure function: no IO, no clock. Always returns a non-empty reason.
//
// Decision tree (first match wins):
//
//	syncedAt == nil               → stale=true,  reason="never"
//	age      >  Ceiling           → stale=true,  reason="expired"
//	StatusAware && isReturning &&
//	            age >  Floor      → stale=true,  reason="status"
//	otherwise                     → stale=false, reason="fresh"
func ttlVerdict(syncedAt *time.Time, status *string, policy TTLPolicy, now time.Time) (bool, string) {
	if syncedAt == nil {
		return true, "never"
	}
	age := now.Sub(*syncedAt)
	if age > policy.Ceiling {
		return true, "expired"
	}
	if policy.StatusAware && isReturning(status) && age > policy.Floor {
		return true, "status"
	}
	return false, "fresh"
}
