package enrichment

import "time"

// TTL returns the refresh interval for the given (source, kind)
// pair per PRD v4 §5.5. A return of 0 means "no TTL applies" —
// either an unknown pair (defensive: callers should not query
// TTL for sources they don't recognise) or a live-only source
// that should not be cached.
//
// Matrix (from PRD §5.5):
//
//	TMDB series continuing : 24h
//	TMDB series ended      : 30d
//	TMDB season active     : 24h
//	TMDB season closed     : 30d
//	TMDB person            : 30d
//	OMDb                   : 24h
//
// The TMDB enums (SourceTMDBSeries / SourceTMDBSeason /
// SourceTMDBPerson) collapse with their respective Kind values.
// Mismatched pairs (e.g., SourceTMDBPerson with KindSeriesEnded)
// fall through to 0 — caller bug, surfaced as no-cache rather
// than silent wrong-TTL.
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
		if kind == KindOMDb {
			return day
		}
	}
	return 0
}
