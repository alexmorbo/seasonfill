// Package clock provides an injectable time source for code that
// needs deterministic tests of timer/ticker/sleep behaviour.
//
// Production callers pass the result of Real() and observe identical
// behaviour to the equivalent direct time package calls: Now()
// delegates to time.Now, NewTimer to time.NewTimer, NewTicker to
// time.NewTicker, Sleep to a time.NewTimer + select on ctx.Done().
// The wrappers are trivial enough for the compiler to inline.
//
// Tests construct a *Fake via NewFake(start). The Fake exposes
// Advance(d) — which moves the virtual clock forward and fires every
// timer/ticker whose deadline is at or before the new now — and
// BlockUntilWaiters(n) — which blocks until at least n goroutines are
// parked in Sleep or on a timer/ticker channel. The two primitives
// together let a test deterministically rendezvous with code that is
// otherwise driven by wall-clock state.
//
// The package introduces no new external dependency. It uses only
// the time, context, sync, sync/atomic, sort stdlib.
package clock
