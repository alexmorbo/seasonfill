package enrichment

import "time"

// TTL returns the refresh interval for the given (source, kind)
// pair per PRD v4 §5.5. A return of 0 means "no TTL applies" —
// either an unknown pair (defensive: callers should not query
// TTL for sources they don't recognise) or a live-only source
// that should not be cached.
//
// Matrix (from PRD §5.5, extended by W18-5 for OMDb age-tiers):
//
//	TMDB series continuing : 24h
//	TMDB series ended      : 30d
//	TMDB season active     : 24h
//	TMDB season closed     : 30d
//	TMDB person            : 30d
//	OMDb (base)            : 24h   ← KEPT for seriesdetail composer
//	OMDb in_production     : 2d    ← W18-5 curve B.1
//	OMDb recent (<1y)      : 7d
//	OMDb mid (1y–3y|unknown): 30d
//	OMDb old (3y–8y)       : 90d
//	OMDb ancient (>8y)     : 180d
//
// The TMDB enums (SourceTMDBSeries / SourceTMDBSeason /
// SourceTMDBPerson) collapse with their respective Kind values.
// Mismatched pairs (e.g., SourceTMDBPerson with KindSeriesEnded)
// fall through to 0 — caller bug, surfaced as no-cache rather
// than silent wrong-TTL. The function stays pure/table-driven:
// the OMDb age-Kind is classified by the caller (worker/SQL), not
// derived from dates here.
func TTL(source Source, kind Kind) time.Duration {
	const (
		day = 24 * time.Hour
	)
	switch source {
	case SourceTMDBSeries:
		switch kind {
		case KindSeriesContinuing:
			return day
		case KindSeriesEnded:
			return 30 * day
		}
	case SourceTMDBSeason:
		switch kind {
		case KindSeasonActive:
			return day
		case KindSeasonClosed:
			return 30 * day
		}
	case SourceTMDBPerson:
		if kind == KindPerson {
			return 30 * day
		}
	case SourceOMDb:
		switch kind {
		case KindOMDb:
			return day // KEPT: composer degraded/freshness projection
		case KindOMDbInProduction:
			return 2 * day
		case KindOMDbRecent:
			return 7 * day
		case KindOMDbMid:
			return 30 * day
		case KindOMDbOld:
			return 90 * day
		case KindOMDbAncient:
			return 180 * day
		}
	}
	return 0
}
