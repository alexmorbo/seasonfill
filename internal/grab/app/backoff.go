package grab

import "time"

// backoffFor returns the sleep duration before attempt N (1-indexed).
// Progression: init → init*5 → init*30, each step capped by max. When init
// is zero, falls back to the original 1s/5s/30s schedule for back-compat.
//
// Tests covering the legacy three-step progression continue to pass when
// they pass `init=0`. Production code (cmd/server/main.go) passes the
// per-instance `Retry.InitialBackoff` (default 1s) so the visible behaviour
// is unchanged unless an operator tunes the knob.
func backoffFor(attempt int, init, max time.Duration) time.Duration {
	if max <= 0 {
		max = 30 * time.Second
	}
	if init <= 0 {
		init = time.Second
	}
	var d time.Duration
	switch attempt {
	case 0, 1:
		d = init
	case 2:
		d = init * 5
	default:
		d = init * 30
	}
	if d > max {
		d = max
	}
	return d
}
