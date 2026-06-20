package enrichment

import "time"

// backoffBase is the floor of the exponential schedule:
// attempts=0 yields a 1h delay (PRD §5.5).
const backoffBase = 1 * time.Hour

// backoffMax is the ceiling (PRD §5.5 — clamp at 24h).
const backoffMax = 24 * time.Hour

// NextAttemptAt returns the wall-clock instant at which a failed
// sync should be retried. Formula per PRD v4 §5.5:
//
//	lastAttempt + min(1h × 2^attempts, 24h)
//
// Monotonic in attempts (each step at least matches the
// previous), saturates at 24h beyond attempts=5 (2^5 = 32h —
// first attempt count that would exceed 24h after the 1h base).
// Negative attempts clamp to 0 — defensive against a worker
// bug; the worker still gets a sensible 1h retry instead of an
// undefined past instant.
//
// lastAttempt is the wall-clock of the failure being scheduled
// from (NOT time.Now() — the function takes the parameter so
// the dispatcher can compute the next attempt relative to the
// original failure even across process restarts and clock skew).
func NextAttemptAt(attempts int, lastAttempt time.Time) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	// Closed-form clamp: shift > 30 risks int overflow on the
	// duration multiplier; saturate well before that.
	if attempts >= 5 {
		return lastAttempt.Add(backoffMax)
	}
	delay := min(backoffBase<<attempts, backoffMax)
	return lastAttempt.Add(delay)
}
