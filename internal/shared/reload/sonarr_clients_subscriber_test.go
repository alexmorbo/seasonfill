package reload

import (
	"context"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

type fakeClient struct{ name string }

func (f *fakeClient) Name() string { return f.name }

func newFakeClient(name string) ports.SonarrClient {
	return &fakeSonarrClient{fakeClient: fakeClient{name: name}}
}

func startClientsSub(t *testing.T, drain time.Duration, boot map[string]ports.SonarrClient, bootCfgs ...map[string]runtime.InstanceSnapshot) (*SonarrClientsSubscriber, *runtime.Bus, context.CancelFunc, *int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	var builds int32
	factory := SonarrClientFactory(func(s runtime.InstanceSnapshot) ports.SonarrClient {
		atomic.AddInt32(&builds, 1)
		return newFakeClient(s.Name)
	})
	sub := NewSonarrClientsSubscriber(boot, factory, slog.Default()).
		WithDrainDelay(drain)
	// Optionally populate initial configs for tests that care about drain-reuse matching
	if len(bootCfgs) > 0 && bootCfgs[0] != nil {
		maps.Copy(sub.configs, bootCfgs[0])
	}
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("sonarr clients subscriber failed to register within 1s")
	}
	return sub, bus, cancel, &builds
}

func TestSonarrClients_AlwaysRebuilds_UnchangedConfig(t *testing.T) {
	t.Parallel()
	boot := map[string]ports.SonarrClient{"alpha": newFakeClient("alpha")}
	cfg := runtime.InstanceSnapshot{
		Name: "alpha", URL: "http://x", APIKey: "k", Timeout: time.Second,
	}
	sub, bus, cancel, builds := startClientsSub(t, 50*time.Millisecond, boot)
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{Instances: []runtime.InstanceSnapshot{cfg}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(builds) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(builds),
		"every publish must rebuild — skip cache was removed to close the secret-rotation regression")
	got, ok := sub.View().ByName("alpha")
	require.True(t, ok)
	assert.NotSame(t, boot["alpha"], got, "rebuild produces a fresh client pointer")
}

func TestSonarrClients_SecretRotation_TriggersRebuild_AndCallback(t *testing.T) {
	t.Parallel()
	bootClient := newFakeClient("Sonarr")
	boot := map[string]ports.SonarrClient{"Sonarr": bootClient}

	var builds atomic.Int32
	factory := SonarrClientFactory(func(s runtime.InstanceSnapshot) ports.SonarrClient {
		builds.Add(1)
		// Capture the api_key the factory received so the test can
		// assert the snapshot's freshly-rotated value flows through.
		return &fakeSonarrClient{fakeClient: fakeClient{name: s.Name + ":" + s.APIKey}}
	})

	var hookMu sync.Mutex
	var hookSnap runtime.Snapshot
	var hookClients map[string]ports.SonarrClient
	var hookCalls atomic.Int32
	onApplied := OnAppliedFunc(func(snap runtime.Snapshot, clients map[string]ports.SonarrClient) {
		hookMu.Lock()
		hookSnap = snap
		hookClients = clients
		hookCalls.Add(1)
		hookMu.Unlock()
	})

	sub := NewSonarrClientsSubscriber(boot, factory, slog.Default()).
		WithDrainDelay(50 * time.Millisecond).
		WithOnApplied(onApplied)

	ctx := t.Context()
	bus := runtime.NewBus(slog.Default())
	t.Cleanup(bus.Close)
	ready := make(chan struct{})
	go sub.Run(ctx, bus, func() { close(ready) })
	<-ready

	rotated := runtime.InstanceSnapshot{
		Name: "Sonarr", URL: "http://sonarr:8989", APIKey: "new-key-32-hex-aaaaaaaaaaaaaaaaa",
		Timeout: time.Second,
	}
	bus.Publish(context.Background(), runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{rotated},
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && hookCalls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}

	require.Equal(t, int32(1), builds.Load(),
		"rotation must invoke the factory exactly once for the rotated instance")
	require.Equal(t, int32(1), hookCalls.Load(),
		"onApplied must fire exactly once for the rotation publish")

	got, ok := sub.View().ByName("Sonarr")
	require.True(t, ok)
	assert.Equal(t, "Sonarr:new-key-32-hex-aaaaaaaaaaaaaaaaa", got.Name(),
		"view must surface the freshly-built client carrying the rotated api_key")
	assert.NotSame(t, bootClient, got, "stale boot client must not be reused")

	hookMu.Lock()
	defer hookMu.Unlock()
	require.Len(t, hookSnap.Instances, 1, "hook receives the publish snapshot verbatim")
	assert.Equal(t, "new-key-32-hex-aaaaaaaaaaaaaaaaa", hookSnap.Instances[0].APIKey,
		"hook's snapshot APIKey must be the rotated value the publish carried")
	require.NotNil(t, hookClients["Sonarr"], "hook must receive the rotated client by name")
	assert.Equal(t, got, hookClients["Sonarr"],
		"hook's client map MUST be identical to View() at the moment apply() ran — no race window")
}

func TestSonarrClients_RemovedInstance_DrainAndDrop(t *testing.T) {
	t.Parallel()
	boot := map[string]ports.SonarrClient{
		"alpha": newFakeClient("alpha"),
		"beta":  newFakeClient("beta"),
	}
	sub, bus, cancel, _ := startClientsSub(t, 100*time.Millisecond, boot)
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{
		Instances: []runtime.InstanceSnapshot{
			{Name: "alpha", URL: "http://a", APIKey: "k"},
		},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("beta"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, ok := sub.View().ByName("beta")
	assert.False(t, ok, "beta must disappear from live View immediately after reload")
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

	bus.Publish(context.Background(), runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("alpha"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
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

func TestSonarrClients_ReAddDuringDrain_ConfigChanged_RebuildsAndDropsPending(t *testing.T) {
	t.Parallel()
	bootClient := newFakeClient("alpha")
	bootCfg := runtime.InstanceSnapshot{Name: "alpha", URL: "http://a", APIKey: "k1"}
	sub, bus, cancel, builds := startClientsSub(t, 500*time.Millisecond,
		map[string]ports.SonarrClient{"alpha": bootClient},
		map[string]runtime.InstanceSnapshot{"alpha": bootCfg})
	defer cancel()

	bus.Publish(context.Background(), runtime.Snapshot{})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.View().ByName("alpha"); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	sub.mu.RLock()
	_, inPending := sub.pendingRemoval["alpha"]
	sub.mu.RUnlock()
	require.True(t, inPending, "alpha must be in pendingRemoval after drain publish")

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
	sub, bus, cancel, _ := startClientsSub(t, 100*time.Millisecond, boot)
	defer cancel()
	bus.Publish(context.Background(), runtime.Snapshot{})
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
	sub := NewSonarrClientsSubscriber(boot, factory, slog.Default()).
		WithDrainDelay(30 * time.Second).
		WithWaitGroup(&wg)
	// Populate initial configs for drain matching
	maps.Copy(sub.configs, cfgs)
	ready := make(chan struct{})
	wg.Go(func() {
		sub.Run(ctx, bus, func() { close(ready) })
	})
	<-ready
	bus.Publish(context.Background(), runtime.Snapshot{})
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
