package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

// fakeCooldownRepo is a minimal in-memory implementation of ports.CooldownRepository.
type fakeCooldownRepo struct {
	sweepErr error
	swept    int64
	calls    int
}

func (f *fakeCooldownRepo) Set(_ context.Context, _ cooldown.Cooldown) error { return nil }
func (f *fakeCooldownRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (f *fakeCooldownRepo) FilterActive(_ context.Context, _ cooldown.Scope, _ []string, _ time.Time) ([]cooldown.Cooldown, error) {
	return nil, nil
}
func (f *fakeCooldownRepo) Sweep(_ context.Context, _ time.Time) (int64, error) {
	f.calls++
	return f.swept, f.sweepErr
}

// compile-time check that fakeCooldownRepo satisfies the interface.
var _ ports.CooldownRepository = (*fakeCooldownRepo)(nil)

// nullLogger returns a slog.Logger that discards all output.
func nullLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError}))
}

// captureLogger returns a logger and a buffer whose String() holds log output.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, buf
}

// --- runCooldownSweep ---

func TestRunCooldownSweep_ExitsOnContextCancel(t *testing.T) {
	t.Parallel()
	repo := &fakeCooldownRepo{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCooldownSweep(ctx, repo, 100*time.Millisecond, nullLogger())
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCooldownSweep did not exit after context cancel")
	}
}

func TestRunCooldownSweep_CallsSweepOnTick(t *testing.T) {
	t.Parallel()
	repo := &fakeCooldownRepo{swept: 3}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCooldownSweep(ctx, repo, 30*time.Millisecond, nullLogger())
	}()

	// Wait for at least one tick.
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if repo.calls == 0 {
		t.Fatal("expected Sweep to be called at least once")
	}
}

func TestRunCooldownSweep_LogsErrorOnSweepFailure(t *testing.T) {
	t.Parallel()
	repo := &fakeCooldownRepo{sweepErr: errors.New("db gone")}
	log, buf := captureLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCooldownSweep(ctx, repo, 30*time.Millisecond, log)
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if repo.calls == 0 {
		t.Fatal("expected Sweep to be called")
	}
	if !bytes.Contains(buf.Bytes(), []byte("cooldown sweep failed")) {
		t.Fatalf("expected error log, got: %s", buf.String())
	}
}

func TestRunCooldownSweep_LogsDebugWhenRowsRemoved(t *testing.T) {
	t.Parallel()
	repo := &fakeCooldownRepo{swept: 5}
	log, buf := captureLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCooldownSweep(ctx, repo, 30*time.Millisecond, log)
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if !bytes.Contains(buf.Bytes(), []byte("cooldown sweep removed")) {
		t.Fatalf("expected debug log for rows removed, got: %s", buf.String())
	}
}

func TestRunCooldownSweep_NoLogWhenZeroRowsRemoved(t *testing.T) {
	t.Parallel()
	repo := &fakeCooldownRepo{swept: 0}
	log, buf := captureLogger()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runCooldownSweep(ctx, repo, 30*time.Millisecond, log)
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if bytes.Contains(buf.Bytes(), []byte("cooldown sweep removed")) {
		t.Fatalf("expected no log when 0 rows removed, got: %s", buf.String())
	}
}

// --- drainBackground ---

func TestDrainBackground_ReturnsWhenWGDoneBeforeTimeout(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(20 * time.Millisecond)
	})

	start := time.Now()
	drainBackground(&wg, 2*time.Second, nullLogger())
	elapsed := time.Since(start)

	// Should return well before the 2s timeout.
	if elapsed > time.Second {
		t.Fatalf("drainBackground blocked too long: %v", elapsed)
	}
}

func TestDrainBackground_ReturnsOnTimeout(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	wg.Add(1)
	// Intentionally never call wg.Done() to force the timeout path.
	defer wg.Done()

	log, buf := captureLogger()
	start := time.Now()
	drainBackground(&wg, 50*time.Millisecond, log)
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Fatalf("drainBackground returned too early: %v", elapsed)
	}
	if !bytes.Contains(buf.Bytes(), []byte("background goroutines did not exit")) {
		t.Fatalf("expected timeout warning log, got: %s", buf.String())
	}
}

func TestDrainBackground_EmptyWGReturnsImmediately(t *testing.T) {
	t.Parallel()
	var wg sync.WaitGroup
	start := time.Now()
	drainBackground(&wg, 2*time.Second, nullLogger())
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("empty WaitGroup should drain immediately")
	}
}

// --- waitForScans ---

// fakeScanner implements scanStateReader.
type fakeScanner struct {
	running  bool
	inflight map[string]uuid.UUID
}

func (f *fakeScanner) IsAnyRunning() bool                  { return f.running }
func (f *fakeScanner) InflightScans() map[string]uuid.UUID { return f.inflight }

var _ scanStateReader = (*fakeScanner)(nil)

// fakeAborter implements scanAborter.
type fakeAborter struct {
	err     error
	aborted []uuid.UUID
	mu      sync.Mutex
}

func (f *fakeAborter) MarkAborted(_ context.Context, id uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborted = append(f.aborted, id)
	return f.err
}

var _ scanAborter = (*fakeAborter)(nil)

func TestWaitForScans_ReturnsImmediatelyWhenNotRunning(t *testing.T) {
	t.Parallel()
	uc := &fakeScanner{running: false}
	repo := &fakeAborter{}
	ctx := context.Background()

	start := time.Now()
	waitForScans(ctx, uc, repo, nullLogger(), 5*time.Second)

	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("should return immediately when no scans running")
	}
	if len(repo.aborted) != 0 {
		t.Fatal("should not call MarkAborted when not running")
	}
}

func TestWaitForScans_MarksAbortedAfterGraceExpires(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	uc := &fakeScanner{
		running:  true,
		inflight: map[string]uuid.UUID{"instance-a": id1},
	}
	repo := &fakeAborter{}
	log, buf := captureLogger()
	ctx := context.Background()

	waitForScans(ctx, uc, repo, log, 50*time.Millisecond)

	repo.mu.Lock()
	aborted := repo.aborted
	repo.mu.Unlock()

	if len(aborted) != 1 || aborted[0] != id1 {
		t.Fatalf("expected id1 to be marked aborted, got: %v", aborted)
	}
	if !bytes.Contains(buf.Bytes(), []byte("scans still in flight")) {
		t.Fatalf("expected warn log, got: %s", buf.String())
	}
}

func TestWaitForScans_LogsErrorWhenMarkAbortedFails(t *testing.T) {
	t.Parallel()
	id1 := uuid.New()
	uc := &fakeScanner{
		running:  true,
		inflight: map[string]uuid.UUID{"instance-b": id1},
	}
	repo := &fakeAborter{err: errors.New("db error")}
	log, buf := captureLogger()
	ctx := context.Background()

	waitForScans(ctx, uc, repo, log, 50*time.Millisecond)

	if !bytes.Contains(buf.Bytes(), []byte("mark aborted failed")) {
		t.Fatalf("expected error log for MarkAborted failure, got: %s", buf.String())
	}
}

func TestWaitForScans_ReturnsAfterScanStops(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	running := true
	uc := &fakeScannerFunc{
		isAnyRunning: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return running
		},
		inflightScans: func() map[string]uuid.UUID { return nil },
	}
	repo := &fakeAborter{}
	ctx := context.Background()

	// Stop the scan after 60ms; grace is 2s.
	go func() {
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		running = false
		mu.Unlock()
	}()

	start := time.Now()
	waitForScans(ctx, uc, repo, nullLogger(), 2*time.Second)
	elapsed := time.Since(start)

	// Should return well before the 2s grace.
	if elapsed > time.Second {
		t.Fatalf("expected early return once scan stopped, took %v", elapsed)
	}
	if len(repo.aborted) != 0 {
		t.Fatal("should not abort when scan stopped within grace")
	}
}

// fakeScannerFunc lets tests inject per-call behaviour.
type fakeScannerFunc struct {
	isAnyRunning  func() bool
	inflightScans func() map[string]uuid.UUID
}

func (f *fakeScannerFunc) IsAnyRunning() bool                  { return f.isAnyRunning() }
func (f *fakeScannerFunc) InflightScans() map[string]uuid.UUID { return f.inflightScans() }

var _ scanStateReader = (*fakeScannerFunc)(nil)
