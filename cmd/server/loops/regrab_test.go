package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// fakeRunner records every RunInstance call and lets tests stub the
// returned QbitError so the streak counter branch is exercised.
type fakeRunner struct {
	mu      sync.Mutex
	calls   map[string]int
	qbitErr map[string]error
	hold    chan struct{}
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		calls:   make(map[string]int),
		qbitErr: make(map[string]error),
	}
}

func (f *fakeRunner) RunInstance(_ context.Context, name domain.InstanceName) (regrab.RunResult, error) {
	if f.hold != nil {
		<-f.hold
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[string(name)]++
	return regrab.RunResult{InstanceName: name, QbitError: f.qbitErr[string(name)]}, nil
}

func (f *fakeRunner) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

// fakeMetrics captures the per-instance streak gauge so tests can
// assert the qbit_unreachable_streak gauge transitions.
type fakeMetrics struct {
	mu      sync.Mutex
	streaks map[string]int
}

func newFakeMetrics() *fakeMetrics {
	return &fakeMetrics{streaks: make(map[string]int)}
}

func (m *fakeMetrics) SetQbitUnreachableStreak(name domain.InstanceName, n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streaks[string(name)] = n
}

func (m *fakeMetrics) streak(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streaks[name]
}

func TestRegrabLoop_StartSpawnsNoGoroutines(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	loop := NewRegrabLoop(r, newFakeMetrics(), nil, slog.Default())

	ctx := t.Context()
	loop.Start(ctx)

	assert.Equal(t, 0, loop.active())
}

func TestRegrabLoop_SwapSettingsBeforeStartIsNoOp(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	loop := NewRegrabLoop(r, newFakeMetrics(), nil, slog.Default())

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: time.Second},
	})

	assert.Equal(t, 0, loop.active())
}

func TestRegrabLoop_SwapSpawnsEnabledLoops(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 10 * time.Millisecond},
		"beta":  {InstanceName: "beta", Enabled: false, PollInterval: 10 * time.Millisecond},
		"gamma": {InstanceName: "gamma", Enabled: true, PollInterval: 0},
	})

	assert.Equal(t, 1, loop.active())
	assert.Equal(t, 10*time.Millisecond, loop.intervalOf("alpha"))

	// Let the timer fire at least once.
	require.Eventually(t, func() bool { return r.count("alpha") >= 1 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestRegrabLoop_SwapStopsDisabledInstance(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx := t.Context()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 50 * time.Millisecond},
	})
	require.Equal(t, 1, loop.active())

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: false, PollInterval: 50 * time.Millisecond},
	})
	assert.Equal(t, 0, loop.active())

	waitWG(t, &bgWG, 2*time.Second)
}

func TestRegrabLoop_SwapStopsRemovedInstance(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 50 * time.Millisecond},
		"beta":  {InstanceName: "beta", Enabled: true, PollInterval: 50 * time.Millisecond},
	})
	require.Equal(t, 2, loop.active())

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 50 * time.Millisecond},
	})
	assert.Equal(t, 1, loop.active())

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestRegrabLoop_SwapRetunesInterval(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: time.Hour},
	})
	require.Equal(t, time.Hour, loop.intervalOf("alpha"))

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 10 * time.Millisecond},
	})
	assert.Equal(t, 10*time.Millisecond, loop.intervalOf("alpha"))

	// Wake nudged the goroutine — it should iterate quickly.
	require.Eventually(t, func() bool { return r.count("alpha") >= 1 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestRegrabLoop_QbitErrorBumpsStreakGauge(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	r.qbitErr["alpha"] = errFakeQbit
	m := newFakeMetrics()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 10 * time.Millisecond},
	})

	require.Eventually(t, func() bool { return m.streak("alpha") >= 2 },
		2*time.Second, 5*time.Millisecond)

	// Flip recovery: clear qbit error, streak should reset on next iter.
	r.mu.Lock()
	delete(r.qbitErr, "alpha")
	r.mu.Unlock()
	require.Eventually(t, func() bool { return m.streak("alpha") == 0 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

func TestRegrabLoop_CtxCancelDrainsGoroutines(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 50 * time.Millisecond},
		"beta":  {InstanceName: "beta", Enabled: true, PollInterval: 50 * time.Millisecond},
	})

	cancel()
	waitWG(t, &bgWG, 2*time.Second)

	// active() count is unaffected by ctx cancellation (loops map is
	// only mutated by SwapSettings), but the goroutines have exited.
}

func TestRegrabLoop_SetIntervalChangesAreApplied(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, newFakeMetrics(), &bgWG, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: time.Hour},
	})

	require.Equal(t, time.Hour, loop.intervalOf("alpha"))

	// Change interval and verify it's updated.
	loop.SwapSettings(map[string]regrab.Settings{
		"alpha": {InstanceName: "alpha", Enabled: true, PollInterval: 10 * time.Millisecond},
	})

	require.Equal(t, 10*time.Millisecond, loop.intervalOf("alpha"))

	// The loop should now iterate faster; we expect multiple calls to RunInstance.
	require.Eventually(t, func() bool { return r.count("alpha") >= 2 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// waitWG blocks until wg.Done is called for every Add, or t fails on
// timeout. Helper kept package-private — sweep_test.go does not need
// it, but torrentsync_test.go shares it (same file-set, same package).
func waitWG(t *testing.T, wg *sync.WaitGroup, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("WaitGroup did not drain in time")
	}
}

// errFakeQbit is a sentinel error used by streak tests so they don't
// have to import a real qBit error type.
var errFakeQbit = fakeQbitErr("fake qbit error")

type fakeQbitErr string

func (e fakeQbitErr) Error() string { return string(e) }

// keep atomic.Int32 alive on builds where the test file is the only
// reference.
var _ atomic.Int32
