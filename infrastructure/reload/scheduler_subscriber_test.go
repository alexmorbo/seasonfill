package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/internal/runtime"
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
	// Poll up to 1s for the rebuild to land.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(builds) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(builds), "schedule change must trigger one rebuild")
	assert.NotSame(t, boot, sub.Current(), "Current must point at the new scheduler")
	if cur := sub.Current(); cur != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)

	// Factory returns a real Scheduler — but the snapshot we publish
	// carries an invalid cron expression, so Start will reject it.
	var factoryCalls int32
	factory := SchedulerFactory(func(schedule string, jitter time.Duration, l *slog.Logger) *scheduler.Scheduler {
		atomic.AddInt32(&factoryCalls, 1)
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
	for time.Now().Before(deadline) && atomic.LoadInt32(&factoryCalls) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&factoryCalls),
		"factory must have been called once")
	// Give apply a beat to finish the error return + runLoop's metric inc.
	time.Sleep(100 * time.Millisecond)

	assert.Same(t, boot, sub.Current(),
		"Start failure must leave the old scheduler running (was the boot one)")
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
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(builds) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
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
