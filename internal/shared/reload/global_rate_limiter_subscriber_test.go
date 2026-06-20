package reload

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/admin/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestGlobalRateLimiter_RebuildsOnChange(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	var builds atomic.Int32
	factory := GlobalLimiterFactory(func(rpm, burst int) *ratelimit.Limiter {
		builds.Add(1)
		return ratelimit.NewFromRPM(rpm, burst)
	})
	sub := NewGlobalRateLimiterSubscriber(&ptr, factory, runtime.RateLimitSnapshot{}, slog.Default())

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("global rate limiter subscriber failed to register within 1s")
	}

	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && builds.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), builds.Load())
	assert.NotNil(t, ptr.Load(), "first publish must populate the atomic")

	// Identical → diff-skip.
	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10}})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), builds.Load(), "diff-skip must NOT rebuild identical limits")

	// Change → rebuild.
	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 60, Burst: 20}})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && builds.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(2), builds.Load())
}

func TestGlobalRateLimiter_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	factory := GlobalLimiterFactory(ratelimit.NewFromRPM)
	sub := NewGlobalRateLimiterSubscriber(&ptr, factory, runtime.RateLimitSnapshot{}, slog.Default())
	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("global rate limiter subscriber failed to register within 1s")
	}

	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 0, Burst: 0}})
	time.Sleep(50 * time.Millisecond)
	assert.Nil(t, ptr.Load(), "0/0 must store nil (unlimited per ratelimit.New contract)")
}
