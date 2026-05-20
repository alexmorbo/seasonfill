package ratelimit

import (
	"context"

	"golang.org/x/time/rate"
)

// Limiter is a thin wrapper around rate.Limiter. The constructor returns nil
// when both rps and burst are zero so call sites can pass it around without
// allocating a no-op limiter. Callers must check for nil before invoking Wait:
//
//	if l != nil { l.Wait(ctx) }
//
// or use the package-level helper `Wait(l, ctx)` which is nil-safe.
type Limiter struct {
	limiter *rate.Limiter
}

// New returns a Limiter. If rps == 0 AND burst == 0 it returns nil
// (interpreted as "unlimited"). If rps > 0 and burst <= 0, burst defaults
// to 1 (matching x/time/rate semantics: burst must be >= 1).
func New(rps float64, burst int) *Limiter {
	if rps == 0 && burst == 0 {
		return nil
	}
	if rps <= 0 {
		// Burst-only allowance is meaningless without a refill rate; fall back
		// to a sensible refill so the limiter does something useful.
		rps = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
}

// NewFromRPM constructs a limiter from requests-per-minute. Returns nil when
// both rpm and burst are zero/negative.
func NewFromRPM(rpm, burst int) *Limiter {
	if rpm <= 0 && burst <= 0 {
		return nil
	}
	rps := float64(rpm) / 60.0
	return New(rps, burst)
}

// Wait blocks until the limiter allows one event, or returns ctx.Err().
// It is a method, not a free function, so the call site must nil-check.
func (l *Limiter) Wait(ctx context.Context) error {
	return l.limiter.Wait(ctx)
}

// Wait is a nil-safe helper. When l is nil the call returns immediately
// (i.e. unlimited). Otherwise it forwards to l.Wait(ctx).
func Wait(l *Limiter, ctx context.Context) error {
	if l == nil {
		return nil
	}
	return l.Wait(ctx)
}
