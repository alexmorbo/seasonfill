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
