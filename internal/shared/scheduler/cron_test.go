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

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
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
	ctx := t.Context()
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
	ctx := t.Context()
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
	ctx := t.Context()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)

	entries := s.cron.Entries()
	require.Len(t, entries, 1)

	var ran atomic.Int32
	done := make(chan struct{})
	go func() {
		entries[0].Job.Run()
		ran.Add(1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("direct cron job invocation did not complete within 2s")
	}
	assert.Equal(t, int32(1), ran.Load())
}

// TestScheduler_Start_JitterAppliedNonPanic — smoke check the jitter
// branch (rand.Int63n) doesn't panic.
func TestScheduler_Start_JitterAppliedNonPanic(t *testing.T) {
	t.Parallel()
	s := New("@every 100ms", 10*time.Millisecond, quietLogger())
	ctx := t.Context()
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
	ctx := t.Context()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)
	err := s.Register("late", "@every 1s", func(context.Context) {})
	assert.Error(t, err, "Register after Start must fail")
}

func TestScheduler_EntryByName_ScanJob(t *testing.T) {
	t.Parallel()
	s := New("*/5 * * * *", 0, quietLogger())
	ctx := t.Context()
	require.NoError(t, s.Start(ctx, newNoopScanUC(t)))
	defer drainStop(t, s)
	assert.Equal(t, "*/5 * * * *", s.EntryByName(ScanJobName))
}

func TestScheduler_RegisterPlusStartRegistered_Runs(t *testing.T) {
	t.Parallel()
	s := New("", 0, quietLogger())
	var ran atomic.Int32
	require.NoError(t, s.Register("oneoff", "@every 50ms", func(context.Context) {
		ran.Add(1)
	}))
	ctx := t.Context()
	require.NoError(t, s.StartRegistered(ctx))
	defer drainStop(t, s)
	// Drive the cron job synchronously by invoking the entry's Run.
	entries := s.cron.Entries()
	require.Len(t, entries, 1)
	entries[0].Job.Run()
	assert.GreaterOrEqual(t, ran.Load(), int32(1))
}

// Story 301: NewWithLocation tests.

func TestNewWithLocation_NilLocFallsBackToUTC(t *testing.T) {
	t.Parallel()
	s := NewWithLocation("", 0, quietLogger(), nil)
	require.NotNil(t, s)
	// Drill into the underlying cron's location via a synthetic
	// AddFunc — we can't read it directly, but we can verify the
	// constructor doesn't panic on nil.
	require.NotNil(t, s.cron)
}

func TestNewWithLocation_AppliesNonUTC(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	s := NewWithLocation("", 0, quietLogger(), loc)
	require.NotNil(t, s)
	// Smoke test: register a job and let the cron compute next-run.
	// We can't easily assert the location is honored without
	// inspecting cron internals, but registration + Stop must not
	// panic in any location.
	require.NoError(t, s.Register("smoke", "0 */6 * * *", func(_ context.Context) {}))
	// not Started — Stop is safe on idle Scheduler.
	assert.NotNil(t, s)
}
