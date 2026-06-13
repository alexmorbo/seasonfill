package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/scan"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// newNoopScanUC builds a real scan.UseCase with zero instances. Run()
// is a no-op (empty instance slice, sync.WaitGroup completes
// immediately). Avoids the fake-scanner ceremony.
func newNoopScanUC(t *testing.T) *scan.UseCase {
	t.Helper()
	lg := quietLogger()
	eval := evaluate.NewPerInstanceUseCase(nil, lg)
	return scan.NewUseCase(nil, eval, nil, lg, true)
}

func TestScheduler_NewStop(t *testing.T) {
	t.Parallel()
	s := New("*/5 * * * *", 0, quietLogger())
	assert.NotNil(t, s)
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(time.Second):
	}
}

func TestScheduler_Start_RegistersEntry(t *testing.T) {
	t.Parallel()
	s := New("*/5 * * * *", 0, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)

	entries := s.cron.Entries()
	require.Len(t, entries, 1, "Start must register exactly one cron entry")
	assert.NotEqual(t, 0, int(s.entryID), "entryID must be a non-zero handle")
}

func TestScheduler_Start_InvalidScheduleReturnsError(t *testing.T) {
	t.Parallel()
	s := New("not a valid cron spec", 0, quietLogger())
	err := s.Start(context.Background(), newNoopScanUC(t))
	assert.Error(t, err, "invalid schedule must surface as a Start error")
}

func TestScheduler_Stop_AfterStart_GracefullyShutsDown(t *testing.T) {
	t.Parallel()
	s := New("@every 100ms", 0, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))

	// Let one tick pass so Stop has something to drain.
	time.Sleep(200 * time.Millisecond)

	stopCtx := s.Stop()
	select {
	case <-stopCtx.Done():
		// expected — no-op scan returns in microseconds
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not signal Done within 2s")
	}
}

// TestScheduler_Start_FireOnce_InvokesScanner — direct-invoke the
// registered cron job to prove the wrapped function is wired and
// non-panic. scan.UseCase has no public counter; we observe the
// completion of Job.Run() instead.
func TestScheduler_Start_FireOnce_InvokesScanner(t *testing.T) {
	t.Parallel()
	s := New("@every 100ms", 0, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)

	entries := s.cron.Entries()
	require.Len(t, entries, 1)

	var ran int32
	done := make(chan struct{})
	go func() {
		entries[0].Job.Run()
		atomic.AddInt32(&ran, 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct cron job invocation did not complete within 2s")
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&ran))
}

// TestScheduler_Start_JitterAppliedNonPanic — smoke check the jitter
// branch (rand.Int63n) doesn't panic.
func TestScheduler_Start_JitterAppliedNonPanic(t *testing.T) {
	t.Parallel()
	s := New("@every 100ms", 10*time.Millisecond, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)

	time.Sleep(200 * time.Millisecond)
	// No assertion — jitter is a non-functional decoration; we only
	// check that the path doesn't panic on a positive jitter value.
}

// drainStop is a test helper — calls Stop and waits up to 1s for the
// returned context to fire.
func drainStop(t *testing.T, s *Scheduler) {
	t.Helper()
	stopCtx := s.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(time.Second):
		t.Log("warning: drainStop timeout; cron may still be running")
	}
}

// Story 211: named-job registry tests.

func TestScheduler_Register_DuplicateRejected(t *testing.T) {
	t.Parallel()
	s := New("", 0, quietLogger())
	require.NoError(t, s.Register("foo", "@every 1s", func(context.Context) {}))
	err := s.Register("foo", "@every 1s", func(context.Context) {})
	assert.Error(t, err, "duplicate name must be rejected")
}

func TestScheduler_RegisterAfterStart_Rejected(t *testing.T) {
	t.Parallel()
	s := New("@every 1s", 0, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)
	err := s.Register("late", "@every 1s", func(context.Context) {})
	assert.Error(t, err, "Register after Start must fail")
}

func TestScheduler_EntryByName_ScanJob(t *testing.T) {
	t.Parallel()
	s := New("*/5 * * * *", 0, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)
	assert.Equal(t, "*/5 * * * *", s.EntryByName(ScanJobName))
}

func TestScheduler_RegisterPlusStartRegistered_Runs(t *testing.T) {
	t.Parallel()
	s := New("", 0, quietLogger())
	var ran int32
	require.NoError(t, s.Register("oneoff", "@every 50ms", func(context.Context) {
		atomic.AddInt32(&ran, 1)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, s.StartRegistered(ctx))
	defer drainStop(t, s)
	// Drive the cron job synchronously by invoking the entry's Run.
	entries := s.cron.Entries()
	require.Len(t, entries, 1)
	entries[0].Job.Run()
	assert.GreaterOrEqual(t, atomic.LoadInt32(&ran), int32(1))
}
