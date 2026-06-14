package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// discardLogger returns a logger that drops every record. Used when the
// test does not assert on log output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// bufferLogger returns a logger + buffer. Caller asserts on buf.String().
func bufferLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, buf
}

func TestLifecycleGroup_DrainsOnCancel(t *testing.T) {
	g := newLifecycleGroup(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	g.Go(ctx, "loop", func(c context.Context) {
		<-c.Done()
	})

	// Cancel triggers the loop to exit; Drain should return nil
	// promptly (well under the 2s timeout).
	cancel()

	start := time.Now()
	if err := g.Drain(2 * time.Second); err != nil {
		t.Fatalf("Drain returned %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Drain blocked too long after cancel: %v", elapsed)
	}
}

func TestLifecycleGroup_Drain_RespectsTimeout(t *testing.T) {
	log, buf := bufferLogger()
	g := newLifecycleGroup(log)

	// A goroutine that ignores ctx — Drain must time out.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	g.Go(context.Background(), "blocker", func(_ context.Context) {
		<-release
	})

	start := time.Now()
	err := g.Drain(50 * time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("Drain returned too early: %v", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Drain blocked beyond timeout: %v", elapsed)
	}
	out := buf.String()
	if !strings.Contains(out, "background drain timed out") {
		t.Fatalf("expected timeout warning, got: %s", out)
	}
	if !strings.Contains(out, "blocker") {
		t.Fatalf("expected still-running name in log, got: %s", out)
	}
}

func TestLifecycleGroup_PanicRecovers(t *testing.T) {
	log, buf := bufferLogger()
	g := newLifecycleGroup(log)

	g.Go(context.Background(), "panicker", func(_ context.Context) {
		panic("boom")
	})

	if err := g.Drain(2 * time.Second); err != nil {
		t.Fatalf("Drain returned %v after panic, want nil", err)
	}

	out := buf.String()
	if !strings.Contains(out, "background goroutine panic") {
		t.Fatalf("expected panic log, got: %s", out)
	}
	if !strings.Contains(out, "panicker") {
		t.Fatalf("expected goroutine name in panic log, got: %s", out)
	}
	if !strings.Contains(out, "boom") {
		t.Fatalf("expected recovered value in log, got: %s", out)
	}
}

func TestLifecycleGroup_TracksPendingNames(t *testing.T) {
	g := newLifecycleGroup(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{}, 3)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	for _, name := range []string{"alpha", "beta", "gamma"} {
		g.Go(ctx, name, func(c context.Context) {
			started <- struct{}{}
			select {
			case <-release:
			case <-c.Done():
			}
		})
	}

	// Wait until all three goroutines are inside fn.
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("goroutine %d did not start within 2s", i)
		}
	}

	got := make(map[string]bool, 3)
	g.pending.Range(func(k, v any) bool {
		name, ok := k.(string)
		if !ok {
			return true
		}
		b, ok := v.(*atomic.Bool)
		if !ok || !b.Load() {
			return true
		}
		got[name] = true
		return true
	})

	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !got[want] {
			t.Fatalf("pending registry missing %q, have: %v", want, got)
		}
	}

	close(release)
	if err := g.Drain(2 * time.Second); err != nil {
		t.Fatalf("Drain after release returned %v, want nil", err)
	}

	// After drain the registry must be empty.
	leftover := 0
	g.pending.Range(func(_, _ any) bool {
		leftover++
		return true
	})
	if leftover != 0 {
		t.Fatalf("pending registry has %d leftover entries, want 0", leftover)
	}
}

func TestLifecycleGroup_ConcurrentGoNoRace(t *testing.T) {
	g := newLifecycleGroup(discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const N = 16
	var spawned sync.WaitGroup
	spawned.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer spawned.Done()
			name := "worker-" + string(rune('a'+(i%26)))
			g.Go(ctx, name, func(c context.Context) {
				<-c.Done()
			})
		}()
	}
	spawned.Wait()
	cancel()

	if err := g.Drain(2 * time.Second); err != nil {
		t.Fatalf("Drain after concurrent Go returned %v, want nil", err)
	}
}
