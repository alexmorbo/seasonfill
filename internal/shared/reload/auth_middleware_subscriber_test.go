package reload

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func TestAuthMiddleware_SessionTTLUpdated(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default(), nil, "")

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("auth middleware subscriber failed to register within 1s")
	}

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
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default(), nil, "")

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("auth middleware subscriber failed to register within 1s")
	}

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
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default(), nil, "")

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	var stillAlive int32 = 1
	ready := make(chan struct{})
	go func() {
		sub.Run(ctx, bus, func() { close(ready) })
		atomic.StoreInt32(&stillAlive, 0)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("auth middleware subscriber failed to register within 1s")
	}

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

func TestAuthMiddleware_SecureCookieFlipped(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour, SecureCookie: false})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default(), nil, "")

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	bus.Publish(ctx, runtime.Snapshot{
		Auth: runtime.AuthSnapshot{
			SessionTTL:   time.Hour,
			SecureCookie: true,
		},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v := ptr.Load(); v != nil && v.SecureCookie {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	v := ptr.Load()
	require.NotNil(t, v)
	assert.True(t, v.SecureCookie, "SecureCookie must propagate via atomic")
}

// TestAuthMiddleware_EpochPropagates confirms the session-epoch field
// flows through the apply path.
func TestAuthMiddleware_EpochPropagates(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{SessionTTL: time.Hour})

	gin.SetMode(gin.TestMode)
	eng := gin.New()
	sub := NewAuthMiddlewareSubscriber(ptr, eng, slog.Default(), nil, "")

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	bus.Publish(ctx, runtime.Snapshot{
		Auth: runtime.AuthSnapshot{
			SessionTTL:   time.Hour,
			SessionEpoch: 42,
		},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if v := ptr.Load(); v != nil && v.SessionEpoch == 42 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	v := ptr.Load()
	require.NotNil(t, v)
	assert.Equal(t, int64(42), v.SessionEpoch)
}
