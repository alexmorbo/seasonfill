// swr_tiers.go pins the per-endpoint TTL table for the TMDB SWR wrapper.
// Order matters: ttlTiers is scanned top-down on every Cache lookup and the
// FIRST prefix match wins. Place specific patterns BEFORE their generic
// parents (e.g. /tv/popular before /tv/).
//
// PLAN-2026-07-01 §5.4 is the source-of-truth document; deviations from that
// table MUST be reflected here AND in the story acceptance criteria.
package tmdb

import "time"

// swrStaleGrace is the window before TTL expiry during which a hit returns
// the cached value INSTANTLY AND triggers a background refresh. Picked as 10%
// of the shortest TTL (15m × 0.10 = 90s) so even the snappiest tier still
// gets a meaningful prefetch window without flapping for tiers where 90s is
// a rounding error (24h → 0.1%). Story 553.
const swrStaleGrace = 90 * time.Second

// swrDefaultTTL is the fallback TTL when the endpoint path matches no tier.
// 1h is the operator-friendly "I didn't think about this endpoint" guess —
// generous enough that an unknown endpoint still caches, tight enough that
// stale data doesn't linger across an operator's debugging session.
const swrDefaultTTL = 1 * time.Hour

// ttlTier is one row of the TTL table. PrefixMatch is a literal string;
// matching is "starts with" (no glob, no regex — predictable). Tier matters
// for the metric label (operators see tmdb_swr_hit_total{tier="<name>"} in
// Grafana — the path string would explode cardinality).
type ttlTier struct {
	PrefixMatch string
	TTL         time.Duration
	Label       string
}

// ttlTiers is the canonical table. Scanned in order; first match wins. Adding
// a new endpoint? Insert ABOVE its generic parent and add a smoke note to the
// story's acceptance criteria. PLAN-2026-07-01 §5.4 is the spec.
var ttlTiers = []ttlTier{
	// Specific /tv/* leaderboards before /tv/<id>.
	{PrefixMatch: "/tv/popular", TTL: 30 * time.Minute, Label: "tv_popular"},
	{PrefixMatch: "/trending/tv/", TTL: 15 * time.Minute, Label: "trending_tv"},
	{PrefixMatch: "/discover/tv", TTL: 30 * time.Minute, Label: "discover_tv"},
	{PrefixMatch: "/search/tv", TTL: 30 * time.Minute, Label: "search_tv"},
	// /tv/<id>/season/<n> before /tv/<id>.
	// Match is by substring: the path is "/tv/<id>/season/<n>", we anchor on
	// "/season/" because a leading /tv/ would also match the parent below.
	// Implemented as a startsWith with the FULL leading "/tv/" prefix that
	// resolveTier checks containment of "/season/" — see resolveTier.
	{PrefixMatch: "/tv/", TTL: 12 * time.Hour, Label: "tv_canon"},
	// Static catalogs.
	{PrefixMatch: "/genre/tv/list", TTL: 24 * time.Hour, Label: "genre_tv_list"},
	{PrefixMatch: "/find/", TTL: 24 * time.Hour, Label: "find_external"},
	// Person canon.
	{PrefixMatch: "/person/", TTL: 12 * time.Hour, Label: "person_canon"},
}

// resolveTier picks the TTL + label for path. NON-MATCH falls to
// (swrDefaultTTL, "default"). The /tv/<id>/season/<n> case shadows the
// generic /tv/ tier — we recognise it via the "/season/" substring AFTER
// the /tv/ prefix match.
func resolveTier(path string) (time.Duration, string) {
	for _, t := range ttlTiers {
		if !startsWith(path, t.PrefixMatch) {
			continue
		}
		// Special-case /tv/<id>/season/<n> — shadowed under the /tv/ tier
		// match above. Distinguish by checking for "/season/" inside the
		// path. The tier label flips to "tv_season" and TTL to 6h.
		if t.Label == "tv_canon" && containsSubstr(path, "/season/") {
			return 6 * time.Hour, "tv_season"
		}
		return t.TTL, t.Label
	}
	return swrDefaultTTL, "default"
}

// startsWith / containsSubstr — tiny helpers so the table file has no
// external imports beyond `time`. Inlined to keep the dependency surface
// minimal (stdlib `strings` is fine but resolveTier is on the hot path of
// every TMDB call — function-call overhead vs strings.HasPrefix is a wash,
// the inline keeps the table file self-contained for review).
func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

func containsSubstr(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
