package seriesdetail

import "time"

// ratingTTLDay is the base unit for the progressive TMDB-rating TTL curve.
const ratingTTLDay = 24 * time.Hour

// tmdbRatingContinuing reports whether the series is still in production
// (or carries a continuing-lifecycle status), which pins the tmdb_rating to
// the shortest refresh window. The status vocabulary mirrors
// enrichment.classifyOMDbKind / classifyKind (series_worker.go) so the TMDB
// rating curve and the OMDb curve classify identical lifecycles identically.
func tmdbRatingContinuing(inProduction bool, status *string) bool {
	if inProduction {
		return true
	}
	if status != nil {
		switch *status {
		case "Returning Series", "In Production", "Pilot", "Planned", "continuing":
			return true
		}
	}
	return false
}

// TMDBRatingTTL returns the progressive age-based refresh interval for a
// series' tmdb_rating, mirroring the OMDb curve shipped in W18-5
// (enrichment.classifyOMDbKind, curve B.1):
//
//	in production / continuing status : 2 days
//	ended, last air < 1y              : 7 days
//	ended, 1y–3y                      : 30 days
//	ended, 3y–8y                      : 90 days
//	ended, > 8y                       : 180 days
//	both last & first air date NULL   : 30 days (unknown age → conservative)
//
// The age metric is LastAirDate, falling back to FirstAirDate. Boundaries are
// strict (After): a series exactly 1y/3y/8y old falls into the OLDER tier —
// identical to W18-5's SQL `>` / `metric.After(now.AddDate(-1,0,0))`.
//
// This helper is consumed ONLY by the W18-7 /ratings TMDB branch. It is
// deliberately private to the rating freshness decision and does NOT retune
// the shared enrichment.TTL(SourceTMDBSeries, …) gate (plan-review F-04).
func TMDBRatingTTL(now time.Time, inProduction bool, status *string, lastAir, firstAir *time.Time) time.Duration {
	if tmdbRatingContinuing(inProduction, status) {
		return 2 * ratingTTLDay
	}

	metric := lastAir
	if metric == nil {
		metric = firstAir
	}
	if metric == nil {
		return 30 * ratingTTLDay // unknown age → conservative Mid, matches W18-5
	}

	switch {
	case metric.After(now.AddDate(-1, 0, 0)):
		return 7 * ratingTTLDay
	case metric.After(now.AddDate(-3, 0, 0)):
		return 30 * ratingTTLDay
	case metric.After(now.AddDate(-8, 0, 0)):
		return 90 * ratingTTLDay
	default:
		return 180 * ratingTTLDay
	}
}

// TMDBRatingStale reports whether a series' tmdb_rating has aged past its
// TTL and should be re-pulled from TMDB. ratingUpdatedAt is the series'
// tmdb_rating_synced_at (Canon.TMDBRatingSyncedAt): a nil pointer
// means the rating was never TMDB-enriched and is treated as stale.
//
// Staleness mirrors the W18-5 worker guard: a row is fresh while
// age < TTL (omdb_worker.go fresh_skip), hence stale ⇔ age >= TTL.
//
// Consumed ONLY by the W18-7 /ratings TMDB branch (F-04: does not touch the
// shared TMDB TTL gate).
func TMDBRatingStale(now time.Time, ratingUpdatedAt *time.Time, inProduction bool, status *string, lastAir, firstAir *time.Time) bool {
	if ratingUpdatedAt == nil {
		return true // never TMDB-enriched → stale
	}
	ttl := TMDBRatingTTL(now, inProduction, status, lastAir, firstAir)
	if ttl <= 0 {
		return true // defensive: curve always returns > 0, but never cache on 0
	}
	return now.Sub(*ratingUpdatedAt) >= ttl
}

// OMDbRatingStale reports whether a series' OMDb-owned ratings (imdb_rating /
// imdb_votes / omdb_rated / omdb_awards) have aged past their progressive TTL and
// should be re-pulled from OMDb. syncedAt is series.enrichment_omdb_synced_at
// (Canon.EnrichmentOMDBSyncedAt); nil ⇒ never OMDb-enriched ⇒ stale.
//
// The W18-5 OMDb curve (enrichment.classifyOMDbKind + TTL(SourceOMDb,kind)) and the
// W18-10 TMDB rating curve (TMDBRatingTTL above) are byte-identical by design —
// same continuing status-list, same 2d/7d/30d/90d/180d age tiers, same strict
// `After` boundaries. So OMDb staleness is exactly TMDB staleness; aliasing here
// REUSES the shipped curve and avoids duplicating the (unexported) classifyOMDbKind
// from internal/enrichment/app. If the two curves ever diverge, split this into a
// dedicated function — but W18-5/W18-10 pin them equal on purpose.
func OMDbRatingStale(now time.Time, syncedAt *time.Time, inProduction bool, status *string, lastAir, firstAir *time.Time) bool {
	return TMDBRatingStale(now, syncedAt, inProduction, status, lastAir, firstAir)
}
