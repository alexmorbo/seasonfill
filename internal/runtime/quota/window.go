package quota

import "time"

// Daily returns the day-boundary truncation of t in the given
// location, then converted to UTC for storage. Two calls within
// the same calendar day in `loc` return the SAME time.Time so the
// (service, window) composite key collides as intended.
//
// loc nil ⇒ time.UTC.
//
// Example: t = 2026-06-15T18:30 UTC, loc = Europe/Moscow (UTC+3)
//
//	→ local = 2026-06-15T21:30 MSK
//	→ trunc = 2026-06-15T00:00 MSK
//	→ return = 2026-06-14T21:00 UTC
//
// For OMDb (UTC-reset) callers pass loc=time.UTC and the round-trip
// is identity at the day boundary.
func Daily(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return dayStart.UTC()
}

// Monthly returns the month-boundary truncation of t in the given
// location, converted to UTC for storage. Same locality semantics
// as Daily.
//
// For services with monthly caps (none today; reserved for SimKL
// + similar). Kept here so future clients don't need to duplicate
// the truncation logic.
func Monthly(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return monthStart.UTC()
}
