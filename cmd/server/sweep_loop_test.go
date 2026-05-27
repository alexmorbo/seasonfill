package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingSweepRepo records every Sweep call as an atomic counter.
// Embeds the test-helpers fakeCooldownRepo's no-op contract via the
// methods we re-declare below — we don't reuse fakeCooldownRepo because
// its counter is a non-atomic int, which would race when the sweep
// goroutine and the test assertion read it concurrently.
type countingSweepRepo struct {
	fakeCooldownRepo
	calls atomic.Int64
}

func (r *countingSweepRepo) Sweep(_ context.Context, _ time.Time) (int64, error) {
	r.calls.Add(1)
	return 0, nil
}

func TestSweepLoop_ReloadShortensCadence(t *testing.T) {
	repo := &countingSweepRepo{}
	s := newSweepLoop(repo, 200*time.Millisecond, nullLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()

	// At 200ms cadence we expect ~0 ticks in 150ms (first tick fires at t=200ms).
	time.Sleep(150 * time.Millisecond)
	slow := repo.calls.Load()
	if slow > 1 {
		t.Fatalf("expected <=1 tick before reload, got %d", slow)
	}

	// Shorten to 50ms; the wake signal should drop the in-flight timer
	// so the next tick fires within ~50ms instead of waiting out the
	// remaining ~50ms of the 200ms window. Over 250ms we expect ~5
	// ticks; allow 3..8 to absorb scheduler jitter under -race.
	s.SetInterval(50 * time.Millisecond)
	start := repo.calls.Load()
	time.Sleep(250 * time.Millisecond)
	delta := repo.calls.Load() - start
	if delta < 3 || delta > 8 {
		t.Fatalf("expected 3..8 ticks after reload to 50ms, got %d", delta)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweepLoop did not exit after context cancel")
	}
}

func TestSweepLoop_DisabledBySetIntervalZero(t *testing.T) {
	repo := &countingSweepRepo{}
	s := newSweepLoop(repo, 30*time.Millisecond, nullLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()

	// Let a few ticks fire.
	time.Sleep(120 * time.Millisecond)
	if repo.calls.Load() == 0 {
		t.Fatal("expected at least one tick before disable")
	}

	// Disable; counter should stop advancing.
	s.SetInterval(0)
	time.Sleep(20 * time.Millisecond) // settle
	frozen := repo.calls.Load()
	time.Sleep(120 * time.Millisecond)
	if repo.calls.Load() != frozen {
		t.Fatalf("expected no ticks while disabled, got %d new", repo.calls.Load()-frozen)
	}

	// Re-enable; should resume.
	s.SetInterval(30 * time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	if repo.calls.Load() == frozen {
		t.Fatal("expected ticks to resume after re-enable")
	}

	cancel()
	<-done
}
