package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestGlobalRateLimiter_RebuildsOnChange(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	var builds int32
	factory := GlobalLimiterFactory(func(rpm, burst int) *ratelimit.Limiter {
		atomic.AddInt32(&builds, 1)
		return ratelimit.NewFromRPM(rpm, burst)
	})
	sub := NewGlobalRateLimiterSubscriber(&ptr, factory, runtime.RateLimitSnapshot{}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	for time.Now().Before(deadline) && atomic.LoadInt32(&builds) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&builds))
	assert.NotNil(t, ptr.Load(), "first publish must populate the atomic")

	// Identical → diff-skip.
	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10}})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), atomic.LoadInt32(&builds), "diff-skip must NOT rebuild identical limits")

	// Change → rebuild.
	bus.Publish(ctx, runtime.Snapshot{GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 60, Burst: 20}})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&builds) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(2), atomic.LoadInt32(&builds))
}

func TestGlobalRateLimiter_ZeroMeansUnlimited(t *testing.T) {
	t.Parallel()
	var ptr atomic.Pointer[ratelimit.Limiter]
	factory := GlobalLimiterFactory(func(rpm, burst int) *ratelimit.Limiter {
		return ratelimit.NewFromRPM(rpm, burst)
	})
	sub := NewGlobalRateLimiterSubscriber(&ptr, factory, runtime.RateLimitSnapshot{}, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
