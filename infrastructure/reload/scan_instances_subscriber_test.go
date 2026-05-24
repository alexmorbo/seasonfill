package reload

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func TestScanInstances_ProviderUpdates(t *testing.T) {
	t.Parallel()
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)
	clients := map[string]ports.SonarrClient{
		"alpha": newFakeClient("alpha"),
		"beta":  newFakeClient("beta"),
	}
	var mu sync.Mutex
	var lastByName map[string]scan.Instance
	sub := NewScanInstancesSubscriber(scanUC,
		func(name string) (ports.SonarrClient, bool) { c, ok := clients[name]; return c, ok },
		func(m map[string]scan.Instance) { mu.Lock(); lastByName = m; mu.Unlock() },
		slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("scan instances subscriber failed to register within 1s")
	}

	bus.Publish(ctx, runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{
			{Name: "alpha", URL: "http://a", APIKey: "k"},
			{Name: "beta", URL: "http://b", APIKey: "k"},
		},
	})
	// Poll for the swap.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ready := len(lastByName) == 2
		mu.Unlock()
		if ready {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	require.Len(t, lastByName, 2)
	mu.Unlock()

	// Drop beta from the next snapshot.
	bus.Publish(ctx, runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{
			{Name: "alpha", URL: "http://a", APIKey: "k"},
		},
	})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		oneLeft := len(lastByName) == 1
		mu.Unlock()
		if oneLeft {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	assert.Len(t, lastByName, 1)
	_, hasBeta := lastByName["beta"]
	assert.False(t, hasBeta, "beta must drop after reload")
	mu.Unlock()
}

func TestScanInstances_MissingClient_Skipped(t *testing.T) {
	t.Parallel()
	scanUC := scan.NewUseCase(nil, nil, nil, slog.Default(), true)
	sub := NewScanInstancesSubscriber(scanUC,
		func(_ string) (ports.SonarrClient, bool) { return nil, false },
		nil, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	defer bus.Close()
	var done int32
	ready := make(chan struct{})
	go func() {
		sub.Run(ctx, bus, func() { close(ready) })
		atomic.StoreInt32(&done, 1)
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("scan instances subscriber failed to register within 1s")
	}
	bus.Publish(ctx, runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{{Name: "ghost"}},
	})
	time.Sleep(50 * time.Millisecond)
	// The subscriber must NOT crash and the metric for errors must
	// stay at zero (the skip is a Warn, not an Error).
	assert.Equal(t, int32(0), atomic.LoadInt32(&done), "Run must still be live after a missing-client publish")
}
