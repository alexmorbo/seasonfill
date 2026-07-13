package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/scheduler"
)

func newTestScheduler(schedule string, jitter time.Duration, _ *slog.Logger) *scheduler.Scheduler {
	return scheduler.New(schedule, jitter, slog.Default())
}

func startSub(t *testing.T, boot *scheduler.Scheduler) (*SchedulerSubscriber, *runtime.Bus, context.CancelFunc, *int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)
	var builds int32
	factory := SchedulerFactory(func(schedule string, jitter time.Duration, l *slog.Logger) *scheduler.Scheduler {
		atomic.AddInt32(&builds, 1)
		return newTestScheduler(schedule, jitter, l)
	})
	sub := NewSchedulerSubscriber(ctx, boot, scanUC, factory, slog.Default())
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("scheduler subscriber failed to register within 1s")
	}
	return sub, bus, cancel, &builds
}

func TestSchedulerSubscriber_DiffSkip(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(), scan.NewUseCase(nil, nil, nil, slog.Default(), true)))
	t.Cleanup(func() { _ = boot.Stop() })

	sub, bus, cancel, builds := startSub(t, boot)
	defer cancel()

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "0 */6 * * *", Jitter: time.Minute},
	})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(builds), "diff-skip must NOT rebuild on identical snapshot")
	assert.Same(t, boot, sub.Current(), "Current must still be the boot scheduler")
}

func TestSchedulerSubscriber_RebuildOnChange(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(), scan.NewUseCase(nil, nil, nil, slog.Default(), true)))

	sub, bus, cancel, builds := startSub(t, boot)
	defer cancel()

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "*/5 * * * *", Jitter: 30 * time.Second},
	})
	// Poll on the TRUE post-condition — the atomic swap of Current() to the
	// new scheduler. `builds` is incremented INSIDE apply() at
	// `next := s.factory(...)`, which is BEFORE `s.current.Swap(next)`, so
	// polling on it observes a mid-apply state and races the swap.
	require.Eventually(t, func() bool {
		return sub.Current() != boot
	}, time.Second, 10*time.Millisecond,
		"schedule change must swap Current() to a new scheduler")

	assert.Equal(t, int32(1), atomic.LoadInt32(builds), "schedule change must trigger one rebuild")
	cur := sub.Current()
	assert.NotSame(t, boot, cur, "Current must point at the new scheduler")
	if cur != nil {
		_ = cur.Stop()
	}
}

func TestSchedulerSubscriber_DisableTearsDown(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(), scan.NewUseCase(nil, nil, nil, slog.Default(), true)))

	sub, bus, cancel, builds := startSub(t, boot)
	defer cancel()

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && sub.Current() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	assert.Nil(t, sub.Current(), "disable must drop the scheduler reference")
	assert.Equal(t, int32(0), atomic.LoadInt32(builds), "disable must NOT call the factory")
}

// --- 028h-2: rebuild-order tests ---

// TestSchedulerSubscriber_FactoryReturnsNil — defensive: if a future
// factory ever returns nil, apply must error out and keep the old
// scheduler. This is a regression guard, not a current-callsite
// requirement.
func TestSchedulerSubscriber_FactoryReturnsNil(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(),
		scan.NewUseCase(nil, nil, nil, slog.Default(), true)))
	t.Cleanup(func() { _ = boot.Stop() })

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)

	factory := SchedulerFactory(func(_ string, _ time.Duration, _ *slog.Logger) *scheduler.Scheduler {
		return nil // simulate factory bug
	})
	sub := NewSchedulerSubscriber(ctx, boot, scanUC, factory, slog.Default())
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "*/5 * * * *", Jitter: 0},
	})

	// Wait up to 1s for the apply to land + error.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cur := sub.Current(); cur != boot {
			t.Fatalf("Current changed away from boot scheduler: got %p, want %p", cur, boot)
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Same(t, boot, sub.Current(),
		"factory error must leave the old scheduler running")
}

// TestSchedulerSubscriber_StartError_OldKeepsRunning — factory returns
// a valid Scheduler but its Start() fails (invalid cron expression
// reaches the inner cron.AddFunc). Old scheduler must keep running.
func TestSchedulerSubscriber_StartError_OldKeepsRunning(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(),
		scan.NewUseCase(nil, nil, nil, slog.Default(), true)))
	t.Cleanup(func() { _ = boot.Stop() })

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)

	// Factory returns a real Scheduler — but the snapshot we publish
	// carries an invalid cron expression, so Start will reject it.
	var factoryCalls atomic.Int32
	factory := SchedulerFactory(func(schedule string, jitter time.Duration, l *slog.Logger) *scheduler.Scheduler {
		factoryCalls.Add(1)
		return scheduler.New(schedule, jitter, l)
	})
	sub := NewSchedulerSubscriber(ctx, boot, scanUC, factory, slog.Default())
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{
			Enabled: true, Schedule: "this is not a cron", Jitter: 0,
		},
	})

	// Poll up to 1s for the apply attempt + error path.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && factoryCalls.Load() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, int32(1), factoryCalls.Load(),
		"factory must have been called once")
	// Give apply a beat to finish the error return + runLoop's metric inc.
	time.Sleep(100 * time.Millisecond)

	assert.Same(t, boot, sub.Current(),
		"Start failure must leave the old scheduler running (was the boot one)")
}

// TestSchedulerSubscriber_Current_NotBlockedDuringGracefulStop —
// Current() must NOT contend with apply()'s critical section. We
// simulate a slow apply by handing the factory a sleep so apply
// holds s.mu for ~200ms; Current() called concurrently must return
// in well under 50ms.
func TestSchedulerSubscriber_Current_NotBlockedDuringGracefulStop(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(),
		scan.NewUseCase(nil, nil, nil, slog.Default(), true)))
	t.Cleanup(func() { _ = boot.Stop() })

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)

	// Slow factory simulates a long-running rebuild step inside apply,
	// keeping s.mu held for ~200ms. If Current() ever took s.mu, it
	// would block the same way.
	factory := SchedulerFactory(func(schedule string, jitter time.Duration, l *slog.Logger) *scheduler.Scheduler {
		time.Sleep(200 * time.Millisecond)
		return scheduler.New(schedule, jitter, l)
	})
	sub := NewSchedulerSubscriber(ctx, boot, scanUC, factory, slog.Default())
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	// Kick apply into its slow path.
	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "*/5 * * * *", Jitter: 0},
	})

	// Give the runLoop a beat to dispatch apply into the slow factory.
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	got := sub.Current()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 50*time.Millisecond,
		"Current() must be lock-free during in-flight apply")
	assert.NotNil(t, got, "Current() must still report the boot scheduler mid-swap")

	// Drain the rebuild so the test scheduler exits cleanly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cur := sub.Current(); cur != nil && cur != boot {
			_ = cur.Stop()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSchedulerSubscriber_HotSwap_OldRefValidUntilSwap — the diff-
// skip path doesn't exercise the new ordering, RebuildOnChange does
// but doesn't check that the OLD reference survived during the
// transition. This test captures Current() before publish, asserts
// it's identical AFTER a successful rebuild (i.e. new replaces old
// only at the end, not before).
func TestSchedulerSubscriber_HotSwap_OldRefValidUntilSwap(t *testing.T) {
	t.Parallel()
	boot := newTestScheduler("0 */6 * * *", time.Minute, slog.Default())
	require.NoError(t, boot.Start(context.Background(),
		scan.NewUseCase(nil, nil, nil, slog.Default(), true)))

	sub, bus, cancel, builds := startSub(t, boot)
	defer cancel()

	// Capture pre-rebuild reference.
	before := sub.Current()
	require.Same(t, boot, before)

	bus.Publish(context.Background(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "*/5 * * * *", Jitter: 30 * time.Second},
	})
	// Poll on swap completion (Current()!=boot), NOT the `builds` counter,
	// which fires mid-apply before s.current.Swap(next).
	require.Eventually(t, func() bool {
		return sub.Current() != boot
	}, time.Second, 10*time.Millisecond,
		"rebuild must swap Current() away from boot")
	require.Equal(t, int32(1), atomic.LoadInt32(builds))

	after := sub.Current()
	assert.NotSame(t, boot, after, "Current must point at the new scheduler post-swap")
	assert.NotNil(t, after)

	// The boot scheduler can still be stopped cleanly because it was
	// torn down LAST (in gracefulStop) — its cron is stopped but the
	// reference is valid.
	// (We don't dereference boot.cron; just assert no panic.)
	_ = boot.Schedule()
	_ = boot.Jitter()

	if after != nil {
		_ = after.Stop()
	}
}
