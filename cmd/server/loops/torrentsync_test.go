package loops

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

type fakeTorrentsyncRunner struct {
	mu        sync.Mutex
	hydrated  map[string]int
	loops     map[string]*fakeTorrentsyncLoop
	hydrateOK bool
}

func newFakeTorrentsyncRunner() *fakeTorrentsyncRunner {
	return &fakeTorrentsyncRunner{
		hydrated:  make(map[string]int),
		loops:     make(map[string]*fakeTorrentsyncLoop),
		hydrateOK: true,
	}
}

func (f *fakeTorrentsyncRunner) Hydrate(_ context.Context, name domain.InstanceName) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hydrated[string(name)]++
	return nil
}

func (f *fakeTorrentsyncRunner) NewLoop(name domain.InstanceName, cadence time.Duration) TorrentsyncRunningLoop {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := &fakeTorrentsyncLoop{name: string(name), cadence: cadence, done: make(chan struct{})}
	f.loops[string(name)] = l
	return l
}

type fakeTorrentsyncLoop struct {
	name    string
	cadence time.Duration
	mu      sync.Mutex
	done    chan struct{}
}

func (l *fakeTorrentsyncLoop) Run(ctx context.Context) {
	<-ctx.Done()
	close(l.done)
}

func (l *fakeTorrentsyncLoop) SetInterval(d time.Duration) {
	l.mu.Lock()
	l.cadence = d
	l.mu.Unlock()
}

func TestTorrentsyncLoop_SwapSpawnsEnabled(t *testing.T) {
	t.Parallel()
	r := newFakeTorrentsyncRunner()
	var bgWG sync.WaitGroup
	loop := NewTorrentsyncLoop(r, &bgWG, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 30 * time.Minute},
		"beta":  {InstanceName: "beta", Enabled: false},
	})

	assert.Equal(t, 1, loop.active())
	assert.Equal(t, 30*time.Minute, loop.cadenceOf("alpha"))

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestTorrentsyncLoop_SubMinuteCadenceFallsBackToDefault(t *testing.T) {
	t.Parallel()
	r := newFakeTorrentsyncRunner()
	var bgWG sync.WaitGroup
	loop := NewTorrentsyncLoop(r, &bgWG, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 0},
	})

	assert.Equal(t, DefaultTorrentsyncCadence, loop.cadenceOf("alpha"))
	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestTorrentsyncLoop_SwapStopsRemoved(t *testing.T) {
	t.Parallel()
	r := newFakeTorrentsyncRunner()
	var bgWG sync.WaitGroup
	loop := NewTorrentsyncLoop(r, &bgWG, slog.Default())
	ctx := t.Context()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 30 * time.Minute},
	})
	require.Equal(t, 1, loop.active())

	loop.SwapSettings(map[string]regrab.Settings{})
	assert.Equal(t, 0, loop.active())

	waitWG(t, &bgWG, 2*time.Second)
}

func TestTorrentsyncLoop_HydrateRunsOncePerSpawn(t *testing.T) {
	t.Parallel()
	r := newFakeTorrentsyncRunner()
	var bgWG sync.WaitGroup
	loop := NewTorrentsyncLoop(r, &bgWG, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 30 * time.Minute},
	})
	// Re-publishing same settings must NOT trigger a second hydrate.
	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 30 * time.Minute},
	})

	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.hydrated["alpha"] == 1
	}, 2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}
