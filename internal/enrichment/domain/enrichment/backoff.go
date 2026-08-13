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

// MaxRetryAttempts is the poison / dead-letter cap for a persistently-failing
// retryable enrichment (E-FIX-1). Once a source's attempts reach this count the
// worker stops scheduling a next_attempt_at — the row is PARKED (terminal) so
// the nightly ListDueForRetry sweep skips it (its next_attempt_at IS NOT NULL
// filter) and the RefreshScheduler already excludes it via the picker's
// attempts>5 guard. 12 sits above the picker's fast-path 5-attempt cutoff (a
// transient fault still gets ~a week of daily retries to self-heal) and below
// the 99 not_found sentinel (parked and not_found stay distinguishable).
const MaxRetryAttempts = 12

// ShouldPark reports whether a retryable failure at this attempt count has
// exhausted the retry budget and must be parked (terminal, no next retry).
func ShouldPark(attempts int) bool {
	return attempts >= MaxRetryAttempts
}
