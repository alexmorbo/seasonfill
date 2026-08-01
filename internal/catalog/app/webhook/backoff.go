package webhook

import "time"

// Webhook-inbox retry backoff — escalating, capped. Kept webhook-local
// on purpose: the inbox retry cadence is independent of the grab /
// enrichment schedules (which have their own backoff shapes). Mirrors
// their escalating-capped form (grab/app/backoff.go,
// enrichment/domain/enrichment/backoff.go) without importing either.
const (
	drainBackoffBase = 2 * time.Second
	drainBackoffMax  = 5 * time.Minute
)

// backoffFor returns the delay before the next attempt, given that
// `attempt` (1-indexed) has just failed. Doubles per attempt from
// drainBackoffBase, saturating at drainBackoffMax:
//
//	attempt 1 -> 2s, 2 -> 4s, 3 -> 8s, ... capped at 5m.
//
// attempt < 1 clamps to 1 (defensive). attempt is clamped before the
// shift so the duration multiply cannot overflow int64.
func backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 20 {
		attempt = 20
	}
	d := drainBackoffBase << (attempt - 1)
	if d <= 0 || d > drainBackoffMax {
		return drainBackoffMax
	}
	return d
}
