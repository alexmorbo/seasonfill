package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func runSecuritySub(t *testing.T, initial bool) (*atomic.Bool, *runtime.Bus, context.CancelFunc) {
	t.Helper()
	var allow atomic.Bool
	allow.Store(initial)
	sub := NewSecuritySubscriber(&allow, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(func() { bus.Close() })

	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("security subscriber failed to register within 1s")
	}
	return &allow, bus, cancel
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

func TestSecurity_FlipsAtomicOnPublish(t *testing.T) {
	t.Parallel()
	allow, bus, cancel := runSecuritySub(t, false)
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{
		Security: runtime.SecuritySnapshot{AllowPrivateTargets: true},
	})
	waitFor(t, func() bool { return allow.Load() })
	assert.True(t, allow.Load())
}

func TestSecurity_FlipsBackToFalse(t *testing.T) {
	t.Parallel()
	allow, bus, cancel := runSecuritySub(t, true)
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{
		Security: runtime.SecuritySnapshot{AllowPrivateTargets: false},
	})
	waitFor(t, func() bool { return !allow.Load() })
	assert.False(t, allow.Load())
}

func TestSecurity_SameValueTwice_NoOp(t *testing.T) {
	t.Parallel()
	allow, bus, cancel := runSecuritySub(t, false)
	defer cancel()
	for i := 0; i < 2; i++ {
		bus.Publish(context.Background(), runtime.Snapshot{
			Security: runtime.SecuritySnapshot{AllowPrivateTargets: false},
		})
		time.Sleep(20 * time.Millisecond)
	}
	assert.False(t, allow.Load())
}

func TestSecurity_StaysAlive_RapidPublishes(t *testing.T) {
	t.Parallel()
	allow, bus, cancel := runSecuritySub(t, false)
	defer cancel()
	for i := 0; i < 50; i++ {
		bus.Publish(context.Background(), runtime.Snapshot{
			Security: runtime.SecuritySnapshot{AllowPrivateTargets: i%2 == 0},
		})
	}
	time.Sleep(100 * time.Millisecond)
	// Final state may be either; only goroutine-survival is asserted via
	// the next successful Publish.
	bus.Publish(context.Background(), runtime.Snapshot{
		Security: runtime.SecuritySnapshot{AllowPrivateTargets: true},
	})
	waitFor(t, func() bool { return allow.Load() })
	require.True(t, allow.Load())
}
