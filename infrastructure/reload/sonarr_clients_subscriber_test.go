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
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("sonarr clients subscriber failed to register within 1s")
	}
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

// TestSonarrClients_ReAddDuringDrain_ConfigChanged_RebuildsAndDropsPending
// covers the OTHER branch of the pendingRemoval lookup: a re-add inside
// the drain window with a CHANGED config must build a fresh client via
// the factory AND drop the pending entry so the old client is not
// silently revived.
func TestSonarrClients_ReAddDuringDrain_ConfigChanged_RebuildsAndDropsPending(t *testing.T) {
	t.Parallel()
	bootClient := newFakeClient("alpha")
	bootCfg := runtime.InstanceSnapshot{Name: "alpha", URL: "http://a", APIKey: "k1"}
	sub, bus, cancel, builds := startClientsSub(t, 500*time.Millisecond,
		map[string]ports.SonarrClient{"alpha": bootClient},
		map[string]runtime.InstanceSnapshot{"alpha": bootCfg})
	defer cancel()

	// Step 1: drain alpha by publishing an empty snapshot.
	bus.Publish(context.Background(), runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("alpha"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Confirm alpha sits in pendingRemoval before we re-add it.
	sub.mu.RLock()
	_, inPending := sub.pendingRemoval["alpha"]
	sub.mu.RUnlock()
	require.True(t, inPending, "alpha must be in pendingRemoval after drain publish")

	// Step 2: re-add alpha with a DIFFERENT api_key, still inside the
	// drain window. apply() must NOT reuse pending.client — it must
	// call the factory and drop the pending entry.
	changed := bootCfg
	changed.APIKey = "k2"
	bus.Publish(context.Background(), runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{changed},
	})

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(builds) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	assert.Equal(t, int32(1), atomic.LoadInt32(builds),
		"changed config during drain MUST invoke the factory")

	got, ok := sub.View().ByName("alpha")
	require.True(t, ok, "alpha must be live again after re-add")
	assert.NotSame(t, bootClient, got,
		"re-add with changed config must surface a NEW client (not the pending one)")

	sub.mu.RLock()
	_, stillPending := sub.pendingRemoval["alpha"]
	sub.mu.RUnlock()
	assert.False(t, stillPending,
		"pendingRemoval entry must be dropped when re-add rebuilds")
}

func TestDrain_SweeperFiresAndCleansUp(t *testing.T) {
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
	// Remove both → 2 pending entries.
	bus.Publish(context.Background(), runtime.Snapshot{})
	// Wait past deadline + one sweeper tick (50ms).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		sub.mu.RLock()
		n := len(sub.pendingRemoval)
		sub.mu.RUnlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sub.mu.RLock()
	pendingCount := len(sub.pendingRemoval)
	sub.mu.RUnlock()
	assert.Equal(t, 0, pendingCount, "sweeper must drop expired entries")
}

func TestDrain_ShutdownFlushesPending(t *testing.T) {
	t.Parallel()
	// Long drain so entries WON'T be expired by wall-clock time.
	boot := map[string]ports.SonarrClient{}
	cfgs := map[string]runtime.InstanceSnapshot{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		boot[n] = newFakeClient(n)
		cfgs[n] = runtime.InstanceSnapshot{Name: n, URL: "http://" + n, APIKey: "k"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	factory := SonarrClientFactory(func(s runtime.InstanceSnapshot) ports.SonarrClient {
		return newFakeClient(s.Name)
	})
	var wg sync.WaitGroup
	sub := NewSonarrClientsSubscriber(boot, cfgs, factory, slog.Default()).
		WithDrainDelay(30 * time.Second). // deliberately long
		WithWaitGroup(&wg)
	ready := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		sub.Run(ctx, bus, func() { close(ready) })
	}()
	<-ready
	// Trigger drain for all five.
	bus.Publish(context.Background(), runtime.Snapshot{})
	// Wait until apply has finished (pending map is populated).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sub.mu.RLock()
		n := len(sub.pendingRemoval)
		sub.mu.RUnlock()
		if n == 5 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Cancel and verify wg.Wait completes within bounded time.
	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("bgWG.Wait blocked after ctx cancel — sweeper goroutine leaked")
	}
	sub.mu.RLock()
	pendingCount := len(sub.pendingRemoval)
	sub.mu.RUnlock()
	assert.Equal(t, 0, pendingCount, "shutdown must flush every pending entry")
}

// panicSub is a minimal apply-shaped subscriber whose 3rd apply panics.
type panicApplier struct{ count int32 }

func (p *panicApplier) apply(_ context.Context, _ runtime.Snapshot) error {
	n := atomic.AddInt32(&p.count, 1)
	if n == 3 {
		panic("synthetic apply panic")
	}
	return nil
}

func TestApply_PanicRecovered(t *testing.T) {
	t.Parallel()
	p := &panicApplier{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	done := make(chan struct{})
	ready := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, bus, "testPanic", slog.Default(), p.apply, func() { close(ready) })
	}()
	<-ready
	// Bus is 1-buffered with latest-wins drop-stale semantics — publishing
	// in a tight loop can squash messages. Publish then wait for count to
	// advance before the next publish so each snapshot is observed.
	waitFor := func(want int32) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if atomic.LoadInt32(&p.count) >= want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	for i := int32(1); i <= 4; i++ {
		bus.Publish(context.Background(), runtime.Snapshot{})
		waitFor(i)
	}
	assert.GreaterOrEqual(t, atomic.LoadInt32(&p.count), int32(4),
		"runLoop must keep processing after a panic in apply")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not exit on ctx cancel")
	}
}
