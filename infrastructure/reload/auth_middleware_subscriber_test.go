package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestAuthMiddleware_SessionTTLUpdated(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	go sub.Run(ctx, bus)
	time.Sleep(10 * time.Millisecond)

	bus.Publish(ctx, runtime.Snapshot{
		Auth: runtime.AuthSnapshot{SessionTTL: 6 * time.Hour, TrustedProxies: []string{"127.0.0.1"}},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v := ptr.Load(); v != nil && v.SessionTTL == 6*time.Hour {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	v := ptr.Load()
	require.NotNil(t, v)
	assert.Equal(t, 6*time.Hour, v.SessionTTL)
}

func TestAuthMiddleware_TrustedProxiesUpdated(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	go sub.Run(ctx, bus)
	time.Sleep(10 * time.Millisecond)

	bus.Publish(ctx, runtime.Snapshot{
		Auth: runtime.AuthSnapshot{
			SessionTTL: time.Hour, TrustedProxies: []string{"10.0.0.0/8"},
		},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		v := ptr.Load()
		if v != nil && len(v.TrustedProxies) == 1 && v.TrustedProxies[0] == "10.0.0.0/8" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	v := ptr.Load()
	require.NotNil(t, v)
	require.Len(t, v.TrustedProxies, 1)
	assert.Equal(t, "10.0.0.0/8", v.TrustedProxies[0])
}

func TestAuthMiddleware_InvalidProxy_FailOpen(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour, TrustedProxies: []string{"127.0.0.1"}})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	var stillAlive int32 = 1
	go func() {
		sub.Run(ctx, bus)
		atomic.StoreInt32(&stillAlive, 0)
	}()
	time.Sleep(10 * time.Millisecond)

	bus.Publish(ctx, runtime.Snapshot{
		Auth: runtime.AuthSnapshot{SessionTTL: time.Hour, TrustedProxies: []string{"not-a-cidr"}},
	})
	time.Sleep(50 * time.Millisecond)
	// Subscriber must NOT die on a SetTrustedProxies error.
	assert.Equal(t, int32(1), atomic.LoadInt32(&stillAlive))
	// New TTL still propagated.
	v := ptr.Load()
	require.NotNil(t, v)
	assert.Equal(t, time.Hour, v.SessionTTL)
}
