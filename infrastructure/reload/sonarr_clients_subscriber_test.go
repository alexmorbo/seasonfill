package reload

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// fakeClient is the smallest ports.SonarrClient possible — only
// Name() is exercised by these tests; the rest are unused stubs
// that satisfy the interface without dragging in real HTTP.
type fakeClient struct{ name string }

func (f *fakeClient) Name() string { return f.name }

func newFakeClient(name string) ports.SonarrClient {
	return &fakeSonarrClient{fakeClient: fakeClient{name: name}}
}

// startClientsSub spins up the subscriber with a short drain delay.
func startClientsSub(t *testing.T, drain time.Duration, boot map[string]ports.SonarrClient, bootCfgs map[string]runtime.InstanceSnapshot) (*SonarrClientsSubscriber, *runtime.Bus, context.CancelFunc, *int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	var builds int32
	factory := SonarrClientFactory(func(s runtime.InstanceSnapshot) ports.SonarrClient {
		atomic.AddInt32(&builds, 1)
		return newFakeClient(s.Name)
	})
	sub := NewSonarrClientsSubscriber(boot, bootCfgs, factory, slog.Default()).
		WithDrainDelay(drain)
	go sub.Run(ctx, bus)
	time.Sleep(10 * time.Millisecond)
	return sub, bus, cancel, &builds
}

func TestSonarrClients_ReuseOnUnchangedConfig(t *testing.T) {
	t.Parallel()
	boot := map[string]ports.SonarrClient{"alpha": newFakeClient("alpha")}
	cfg := runtime.InstanceSnapshot{
		Name: "alpha", URL: "http://x", APIKey: "k", Timeout: time.Second,
	}
	sub, bus, cancel, builds := startClientsSub(t, 50*time.Millisecond,
		boot, map[string]runtime.InstanceSnapshot{"alpha": cfg})
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{Instances: []runtime.InstanceSnapshot{cfg}})
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(builds), "unchanged config must NOT rebuild client")
	got, ok := sub.View().ByName("alpha")
	require.True(t, ok)
	assert.Equal(t, boot["alpha"], got, "must reuse the exact same client pointer")
}

func TestSonarrClients_RebuildOnConfigChange(t *testing.T) {
	t.Parallel()
	boot := map[string]ports.SonarrClient{"alpha": newFakeClient("alpha")}
	cfg := runtime.InstanceSnapshot{Name: "alpha", URL: "http://x", APIKey: "k1"}
	sub, bus, cancel, builds := startClientsSub(t, 50*time.Millisecond,
		boot, map[string]runtime.InstanceSnapshot{"alpha": cfg})
	defer cancel()
	changed := cfg
	changed.APIKey = "k2"
	bus.Publish(context.Background(), runtime.Snapshot{Instances: []runtime.InstanceSnapshot{changed}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(builds) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(builds), "api_key change must rebuild")
	got, ok := sub.View().ByName("alpha")
	require.True(t, ok)
	assert.NotSame(t, boot["alpha"], got, "client pointer must change after rebuild")
}

func TestSonarrClients_RemovedInstance_DrainAndDrop(t *testing.T) {
	t.Parallel()
	boot := map[string]ports.SonarrClient{
		"alpha": newFakeClient("alpha"),
		"beta":  newFakeClient("beta"),
	}
	cfgs := map[string]runtime.InstanceSnapshot{
		"alpha": {Name: "alpha", URL: "http://a", APIKey: "k"},
		"beta":  {Name: "beta", URL: "http://b", APIKey: "k"},
	}
	sub, bus, cancel, _ := startClientsSub(t, 100*time.Millisecond, boot, cfgs)
	defer cancel()
	// Remove beta; alpha remains.
	bus.Publish(context.Background(), runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{cfgs["alpha"]},
	})
	// Immediately after publish: beta gone from View, but client is
	// still in pendingRemoval until 100ms elapses.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("beta"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, ok := sub.View().ByName("beta")
	assert.False(t, ok, "beta must disappear from live View immediately after reload")
	// Wait past drain.
	time.Sleep(150 * time.Millisecond)
	sub.mu.Lock()
	pendingCount := len(sub.pendingRemoval)
	sub.mu.Unlock()
	assert.Equal(t, 0, pendingCount, "pendingRemoval must drain after delay")
}

func TestSonarrClients_ReAddDuringDrain_ReusesPending(t *testing.T) {
	t.Parallel()
	bootClient := newFakeClient("alpha")
	cfg := runtime.InstanceSnapshot{Name: "alpha", URL: "http://a", APIKey: "k"}
	sub, bus, cancel, builds := startClientsSub(t, 500*time.Millisecond,
		map[string]ports.SonarrClient{"alpha": bootClient},
		map[string]runtime.InstanceSnapshot{"alpha": cfg})
	defer cancel()

	// Step 1: delete alpha.
	bus.Publish(context.Background(), runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("alpha"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Step 2: re-add alpha with identical config inside the drain.
	bus.Publish(context.Background(), runtime.Snapshot{Instances: []runtime.InstanceSnapshot{cfg}})
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c, ok := sub.View().ByName("alpha"); ok && c == bootClient {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, ok := sub.View().ByName("alpha")
	require.True(t, ok)
	assert.Equal(t, bootClient, got, "re-add during drain must reuse the pending client (no double-construct)")
	assert.Equal(t, int32(0), atomic.LoadInt32(builds), "factory must NOT be called on re-add reuse")
}
