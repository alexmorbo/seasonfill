package loops

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordingPoller counts Poll calls and optionally returns a fixed error to
// prove a failed tick does not kill the loop.
type recordingPoller struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (r *recordingPoller) Poll(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.err
}

func (r *recordingPoller) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// A nil poller must return immediately without panic.
func TestRunChanges_NilPoller_NoOp(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunChanges(context.Background(), nil, time.Hour, nil)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunChanges(nil) did not return promptly")
	}
}

// An already-cancelled ctx must make RunChanges return during the startup
// delay, before ever calling Poll.
func TestRunChanges_CancelledCtx_NoPoll(t *testing.T) {
	defer SetChangesStartupDelayForTest(50 * time.Millisecond)()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	rec := &recordingPoller{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunChanges(ctx, rec, 10*time.Millisecond, nil)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunChanges did not honour cancelled ctx")
	}
	if got := rec.count(); got != 0 {
		t.Fatalf("Poll called %d times on cancelled ctx; want 0", got)
	}
}

// With a shrunk startup delay + short interval, RunChanges fires the first
// tick then ticks; Poll errors must NOT kill the loop; cancel drains cleanly.
func TestRunChanges_TicksAndDrains(t *testing.T) {
	defer SetChangesStartupDelayForTest(5 * time.Millisecond)()

	rec := &recordingPoller{err: errors.New("boom")} // errors must not kill the loop
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunChanges(ctx, rec, 15*time.Millisecond, nil)
	}()

	time.Sleep(150 * time.Millisecond) // startup(5ms) + several 15ms ticks
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunChanges did not drain on cancel")
	}
	if got := rec.count(); got < 2 {
		t.Fatalf("expected >=2 polls (startup + ticks), got %d", got)
	}
}
