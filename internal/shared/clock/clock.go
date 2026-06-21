package clock

import (
	"context"
	"time"
)

// Clock is an injectable time source. Production code receives the
// result of Real(); test code receives a *Fake whose virtual time
// only moves when the test calls Advance(d).
//
// Every method is safe for concurrent use.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep blocks for d, or until ctx is cancelled, whichever wins.
	// Returns ctx.Err() on cancel; nil otherwise. d <= 0 returns nil
	// immediately.
	Sleep(ctx context.Context, d time.Duration) error
	// NewTimer returns a Timer that fires once after d.
	NewTimer(d time.Duration) Timer
	// NewTicker returns a Ticker that fires every d.
	NewTicker(d time.Duration) Ticker
}

// Timer mirrors *time.Timer's subset that production callers use.
// The channel returned by C() carries one tick (zero-value time.Time
// for fake clocks; the real-clock equivalent carries the actual fire
// time, matching time.Timer semantics).
type Timer interface {
	// C returns the channel on which the tick is delivered.
	C() <-chan time.Time
	// Stop halts the timer if it has not yet fired. Returns true when
	// the call stopped the timer before it fired, false otherwise
	// (matching time.Timer.Stop semantics).
	Stop() bool
}

// Ticker mirrors *time.Ticker's subset that production callers use.
type Ticker interface {
	// C returns the channel on which ticks are delivered.
	C() <-chan time.Time
	// Stop halts the ticker. Safe to call multiple times.
	Stop()
}

// Real returns a Clock backed by the time package. The returned
// value is stateless (a singleton); callers may keep a reference
// for the entire process lifetime.
func Real() Clock { return realClock{} }

// realClock is the production implementation. Each method is a
// 1:1 alias for the equivalent time-package call so the compiler
// can inline them.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (realClock) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }

func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{t: time.NewTicker(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
