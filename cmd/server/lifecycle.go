package main

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// lifecycleGroup is a small wrapper around sync.WaitGroup that adds
// goroutine-name bookkeeping (for diagnostic logging on Drain timeout)
// and panic recovery (matches the pre-existing bgWG-based behaviour:
// each background loop has its own panic boundary and a panic must
// NOT crash the process — we log + continue).
//
// API surface is intentionally tiny: Go(ctx, name, fn) + Drain(timeout).
// Adding more methods (e.g. a "running count" getter) is a code smell:
// the registry is internal diagnostic state, not a public observable.
type lifecycleGroup struct {
	log *slog.Logger

	wg      sync.WaitGroup
	pending sync.Map // name(string) -> *atomic.Bool (true = running)
}

// newLifecycleGroup returns a ready-to-use group. log is required —
// panic recovery + Drain timeout warnings emit through it.
func newLifecycleGroup(log *slog.Logger) *lifecycleGroup {
	return &lifecycleGroup{log: log}
}

// Go spawns fn in a new goroutine, tracking it in the wait group +
// pending registry. The name is used purely for diagnostic logging
// (Drain timeout + panic recovery); callers should choose a stable
// string per call site. Duplicate names are tolerated but will
// overwrite the previous registry entry — keep names unique per
// concurrent execution.
//
// Panics in fn are recovered: the panic value + stack are logged at
// Error level and the goroutine returns cleanly. This matches the
// behaviour of the previous open-coded `defer bgWG.Done()` pattern
// where a panic in a background loop did not propagate to the
// process; instead the wait group counter was released and the
// shutdown ladder continued.
func (g *lifecycleGroup) Go(ctx context.Context, name string, fn func(context.Context)) {
	g.wg.Add(1)
	running := &atomic.Bool{}
	running.Store(true)
	g.pending.Store(name, running)

	go func() {
		defer g.wg.Done()
		defer func() {
			running.Store(false)
			g.pending.Delete(name)
			if r := recover(); r != nil {
				g.log.Error("background goroutine panic",
					slog.String("name", name),
					slog.Any("recover", r),
					slog.String("stack", string(debug.Stack())),
				)
			}
		}()
		fn(ctx)
	}()
}

// Drain blocks until every goroutine spawned via Go exits, or until
// timeout elapses. Returns nil on clean drain; on timeout, returns
// context.DeadlineExceeded and emits a Warn log enumerating the names
// of goroutines still running. (Returning context.DeadlineExceeded
// matches the stdlib convention for deadline-driven failures; callers
// can errors.Is the sentinel without depending on a package-local
// error value.)
func (g *lifecycleGroup) Drain(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		var names []string
		g.pending.Range(func(k, v any) bool {
			b, ok := v.(*atomic.Bool)
			if !ok || !b.Load() {
				return true
			}
			s, ok := k.(string)
			if !ok {
				return true
			}
			names = append(names, s)
			return true
		})
		g.log.Warn("background drain timed out",
			slog.Duration("timeout", timeout),
			slog.Any("still_running", names),
		)
		return context.DeadlineExceeded
	}
}
