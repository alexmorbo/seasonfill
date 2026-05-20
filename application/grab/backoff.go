package grab

import "time"

// backoffFor returns the sleep duration before attempt N (1-indexed).
// Sequence: 1s, 5s, 30s — capped by max. Attempts beyond the third still
// receive `max` so callers can keep the schedule going if desired.
func backoffFor(attempt int, max time.Duration) time.Duration {
	if max <= 0 {
		max = 30 * time.Second
	}
	var d time.Duration
	switch attempt {
	case 0, 1:
		d = time.Second
	case 2:
		d = 5 * time.Second
	default:
		d = 30 * time.Second
	}
	if d > max {
		d = max
	}
	return d
}
