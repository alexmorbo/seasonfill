package app

import "time"

// Notification-dispatch retry backoff — escalating, capped. Kept
// package-local on purpose (mirrors the webhook-inbox drainer's
// backoff): the notification retry cadence is independent of the
// grab / enrichment / webhook schedules.
const (
	dispatchBackoffBase = 2 * time.Second
	dispatchBackoffMax  = 5 * time.Minute
)

// backoffFor returns the delay before the next attempt, given that
// `attempt` (1-indexed) has just failed. Doubles per attempt from
// dispatchBackoffBase, saturating at dispatchBackoffMax:
//
//	attempt 1 -> 2s, 2 -> 4s, 3 -> 8s, ... capped at 5m.
//
// attempt < 1 clamps to 1 (defensive); the shift is clamped before the
// multiply so the duration cannot overflow int64.
func backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 20 {
		attempt = 20
	}
	d := dispatchBackoffBase << (attempt - 1)
	if d <= 0 || d > dispatchBackoffMax {
		return dispatchBackoffMax
	}
	return d
}
