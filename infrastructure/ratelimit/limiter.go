package ratelimit

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// Observer is invoked by Wait exactly once when a call would have blocked
// (i.e. the limiter's reservation reported a non-zero delay). It is NOT
// invoked on the instant-acquire fast path. The scope argument is the
// label value bound to the observer at construction via WithObserver —
// typically "per_instance" or "global".
//
// The observer must be cheap and non-blocking. It runs on the caller's
// goroutine before the wait sleeps. Panics in the observer propagate to
// the caller.
type Observer func(scope string)

// Limiter is a thin wrapper around rate.Limiter. The constructor returns nil
// when both rps and burst are zero so call sites can pass it around without
// allocating a no-op limiter. Callers must check for nil before invoking Wait:
//
//	if l != nil { l.Wait(ctx) }
//
// or use the package-level helper `Wait(l, ctx)` which is nil-safe.
type Limiter struct {
	limiter  *rate.Limiter
	observer Observer
	scope    string
}

// Option configures a Limiter at construction.
type Option func(*Limiter)

// WithObserver binds an observer callback and a scope label. When the
// limiter's Wait would block, observer(scope) is invoked once before
// the sleep. Passing a nil observer is a no-op (the limiter behaves
// as if no option was supplied).
func WithObserver(scope string, observer Observer) Option {
	return func(l *Limiter) {
		if observer == nil {
			return
		}
		l.observer = observer
		l.scope = scope
	}
}

// New returns a Limiter. If rps == 0 AND burst == 0 it returns nil
// (interpreted as "unlimited"). If rps > 0 and burst <= 0, burst defaults
// to 1 (matching x/time/rate semantics: burst must be >= 1).
func New(rps float64, burst int) *Limiter {
	return NewWithOptions(rps, burst)
}

// NewFromRPM constructs a limiter from requests-per-minute. Returns nil when
// both rpm and burst are zero/negative.
func NewFromRPM(rpm, burst int) *Limiter {
	return NewFromRPMWithOptions(rpm, burst)
}

// NewWithOptions returns a Limiter configured with the given options. Same
// zero-rps/zero-burst nil rule as New.
func NewWithOptions(rps float64, burst int, opts ...Option) *Limiter {
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
	l := &Limiter{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
	for _, o := range opts {
		o(l)
	}
	return l
}

// NewFromRPMWithOptions constructs a limiter from requests-per-minute with
// optional observer wiring. Returns nil when both rpm and burst are
// zero/negative.
func NewFromRPMWithOptions(rpm, burst int, opts ...Option) *Limiter {
	if rpm <= 0 && burst <= 0 {
		return nil
	}
	rps := float64(rpm) / 60.0
	return NewWithOptions(rps, burst, opts...)
}

// Wait blocks until the limiter allows one event, or returns ctx.Err().
// It is a method, not a free function, so the call site must nil-check.
//
// Wait uses Reserve+Delay to detect whether the call would block. When
// the reported delay is non-zero, the observer (if any) fires once with
// the bound scope label before the sleep. ctx cancellation cancels the
// reservation so the consumed token is returned to the limiter's bucket.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	r := l.limiter.ReserveN(now, 1)
	if !r.OK() {
		// Burst < 1 cannot happen given the constructor guards, but keep
		// the branch defensive — falling back to the upstream Wait
		// preserves the original error path if the invariant is ever
		// broken (e.g. by a future refactor of the constructor).
		return l.limiter.Wait(ctx)
	}
	delay := r.DelayFrom(now)
	if delay <= 0 {
		return nil
	}
	if l.observer != nil {
		l.observer(l.scope)
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		r.Cancel()
		return ctx.Err()
	}
}

// Wait is a nil-safe helper. When l is nil the call returns immediately
// (i.e. unlimited). Otherwise it forwards to l.Wait(ctx).
func Wait(l *Limiter, ctx context.Context) error {
	if l == nil {
		return nil
	}
	return l.Wait(ctx)
}
