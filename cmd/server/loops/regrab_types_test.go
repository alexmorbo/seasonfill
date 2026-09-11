package loops

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// regrabName is a readability alias for the typed instance name carried by
// regrab.Settings.
func regrabName(s string) domain.InstanceName { return domain.InstanceName(s) }

// fakeInstanceTypes is a stub InstanceTypeSource. A nil map models "the
// resolver knows nothing yet" (boot-order race); an absent key models "this
// name is unknown to the resolver".
type fakeInstanceTypes struct {
	mu    sync.Mutex
	types map[string]string
	calls int
}

func newFakeInstanceTypes(types map[string]string) *fakeInstanceTypes {
	return &fakeInstanceTypes{types: types}
}

func (f *fakeInstanceTypes) InstanceTypes() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	out := make(map[string]string, len(f.types))
	maps.Copy(out, f.types)
	return out
}

func (f *fakeInstanceTypes) set(name, typ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.types[name] = typ
}

// captureLogger returns a logger writing JSON into a buffer plus a closure
// counting how many times `event` appears as an "msg" value.
func captureLogger() (*slog.Logger, func(event string) int) {
	buf := &strings.Builder{}
	sw := newSyncWriter(buf)
	h := slog.NewJSONHandler(sw, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), func(event string) int {
		return strings.Count(sw.snapshot(), `"msg":"`+event+`"`)
	}
}

// syncWriter serialises writes from the loop goroutines into the strings
// builder so -race stays quiet.
type syncWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func newSyncWriter(w *strings.Builder) *syncWriter { return &syncWriter{w: w} }

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// snapshot reads the accumulated output under the same mutex the writes
// take — reading the builder directly would race with a loop goroutine.
func (s *syncWriter) snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.String()
}

func enabled(name string, d time.Duration) regrab.Settings {
	return regrab.Settings{InstanceName: regrabName(name), Enabled: true, PollInterval: d}
}

// Test 1 — a radarr instance never gets a loop, is logged exactly once and
// raises the gauge.
func TestRegrabLoop_SkipsUnsupportedInstanceType(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{"radarr": "radarr"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"radarr": enabled("radarr", 10*time.Millisecond),
	})

	assert.Equal(t, 0, loop.active(), "no per-instance loop may exist for an unsupported type")
	assert.Equal(t, []string{"radarr"}, loop.skippedNames())
	assert.Equal(t, 1, count("regrab_skipped_unsupported_type"))
	v, ok := m.unresolvedGauge("radarr")
	assert.True(t, ok, "gauge must be published for the skipped instance")
	assert.Equal(t, 1, v)

	// The loop must never have been run.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, r.count("radarr"))

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// Test 2 — repeated snapshots must NOT repeat the INFO (dedup), and must
// not resurrect the loop.
func TestRegrabLoop_SkipLogIsDeduplicatedAcrossSwaps(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{"radarr": "radarr"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	next := map[string]regrab.Settings{"radarr": enabled("radarr", 10*time.Millisecond)}
	for range 5 {
		loop.SwapSettings(next)
	}

	assert.Equal(t, 1, count("regrab_skipped_unsupported_type"),
		"five reload snapshots must produce exactly one INFO, not a new log flood")
	assert.Equal(t, 0, loop.active())
	v, _ := m.unresolvedGauge("radarr")
	assert.Equal(t, 1, v)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// Test 3 — regression guard on the main path: a sonarr instance still
// spawns, is never logged as skipped, and never raises the gauge.
func TestRegrabLoop_SupportedInstanceTypeStillSpawns(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{"homelab": "sonarr"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"homelab": enabled("homelab", 10*time.Millisecond),
	})

	assert.Equal(t, 1, loop.active())
	assert.Empty(t, loop.skippedNames())
	assert.Equal(t, 0, count("regrab_skipped_unsupported_type"))
	_, ok := m.unresolvedGauge("homelab")
	assert.False(t, ok, "the gauge must not be published for a supported instance")

	require.Eventually(t, func() bool { return r.count("homelab") >= 1 },
		2*time.Second, 5*time.Millisecond)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// Test 4 — ★ FAIL-OPEN. Several shapes of "cannot resolve the type" must all
// behave exactly as the pre-F2 code did: spawn the loop, log nothing.
//
// This is the single most important test in the story. The opposite
// behaviour would silently disable regrab in production on any boot-order
// race, and regrab is seasonfill's core value on top of Sonarr.
func TestRegrabLoop_FailsOpenWhenTypeUnknown(t *testing.T) {
	t.Parallel()

	cases := map[string]InstanceTypeSource{
		"nil resolver":      nil,
		"empty snapshot":    newFakeInstanceTypes(map[string]string{}),
		"name not resolved": newFakeInstanceTypes(map[string]string{"someone_else": "radarr"}),
		"empty type value":  newFakeInstanceTypes(map[string]string{"homelab": ""}),
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner()
			m := newFakeMetrics()
			log, count := captureLogger()
			var bgWG sync.WaitGroup
			loop := NewRegrabLoop(r, m, &bgWG, log)
			if src != nil {
				loop = loop.WithInstanceTypes(src)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			loop.Start(ctx)

			loop.SwapSettings(map[string]regrab.Settings{
				"homelab": enabled("homelab", 10*time.Millisecond),
			})

			assert.Equal(t, 1, loop.active(),
				"an unresolvable instance type MUST fail open and spawn the loop")
			assert.Empty(t, loop.skippedNames())
			assert.Equal(t, 0, count("regrab_skipped_unsupported_type"))
			_, ok := m.unresolvedGauge("homelab")
			assert.False(t, ok)

			cancel()
			waitWG(t, &bgWG, 2*time.Second)
		})
	}
}

// Test 5 — the instance leaves settings: gauge resets to 0 and the dedup
// memory is cleared, so a later reappearance logs again.
func TestRegrabLoop_SkippedInstanceRemovedResetsGaugeAndDedup(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{"radarr": "radarr"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	radarrOnly := map[string]regrab.Settings{"radarr": enabled("radarr", 10*time.Millisecond)}
	loop.SwapSettings(radarrOnly)
	require.Equal(t, 1, count("regrab_skipped_unsupported_type"))

	// Instance disappears from qbit_settings.
	loop.SwapSettings(map[string]regrab.Settings{})
	v, ok := m.unresolvedGauge("radarr")
	require.True(t, ok)
	assert.Equal(t, 0, v, "the F4 rule is `> 0`; a latched 1 would alert forever")
	assert.Empty(t, loop.skippedNames())

	// Reappears — the INFO must be written again (this is a new transition).
	loop.SwapSettings(radarrOnly)
	assert.Equal(t, 2, count("regrab_skipped_unsupported_type"))
	v, _ = m.unresolvedGauge("radarr")
	assert.Equal(t, 1, v)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// Test 5b — a running loop whose instance CHANGES type must be torn down,
// and a skipped instance that becomes supported must start running. This is
// the only path on which the gate has to cancel an existing goroutine.
func TestRegrabLoop_TypeChangeStartsAndStopsLoops(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, _ := captureLogger()
	var bgWG sync.WaitGroup
	types := newFakeInstanceTypes(map[string]string{"shifty": "sonarr"})
	loop := NewRegrabLoop(r, m, &bgWG, log).WithInstanceTypes(types)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	next := map[string]regrab.Settings{"shifty": enabled("shifty", time.Hour)}
	loop.SwapSettings(next)
	require.Equal(t, 1, loop.active())

	// Operator flips the instance to radarr.
	types.set("shifty", "radarr")
	loop.SwapSettings(next)
	assert.Equal(t, 0, loop.active(), "a type change must tear the goroutine down")
	v, _ := m.unresolvedGauge("shifty")
	assert.Equal(t, 1, v)

	// ...and back to sonarr.
	types.set("shifty", "sonarr")
	loop.SwapSettings(next)
	assert.Equal(t, 1, loop.active())
	v, _ = m.unresolvedGauge("shifty")
	assert.Equal(t, 0, v)
	assert.Empty(t, loop.skippedNames())

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// Test 6 — ★ the real production snapshot: homelab (sonarr) + radarr in the
// SAME map. The sonarr loop must be alive, the radarr one must not exist.
func TestRegrabLoop_MixedSnapshotKeepsSonarrSkipsRadarr(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{
			"homelab": "sonarr",
			"radarr":  "radarr",
		}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"homelab": enabled("homelab", 10*time.Millisecond),
		"radarr":  enabled("radarr", 10*time.Millisecond),
	})

	assert.Equal(t, 1, loop.active(), "exactly one loop: homelab")
	assert.Equal(t, 10*time.Millisecond, loop.intervalOf("homelab"))
	assert.Equal(t, time.Duration(0), loop.intervalOf("radarr"))
	assert.Equal(t, []string{"radarr"}, loop.skippedNames())
	assert.Equal(t, 1, count("regrab_skipped_unsupported_type"))

	require.Eventually(t, func() bool { return r.count("homelab") >= 1 },
		2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 0, r.count("radarr"))

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}

// A disabled row of an unsupported type raises nothing: it was never going
// to spawn a loop, so a gauge + log about it would be pure noise.
func TestRegrabLoop_DisabledUnsupportedRowIsNotFlagged(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	m := newFakeMetrics()
	log, count := captureLogger()
	var bgWG sync.WaitGroup
	loop := NewRegrabLoop(r, m, &bgWG, log).
		WithInstanceTypes(newFakeInstanceTypes(map[string]string{"radarr": "radarr"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop.Start(ctx)

	loop.SwapSettings(map[string]regrab.Settings{
		"radarr": {InstanceName: regrabName("radarr"), Enabled: false, PollInterval: time.Minute},
	})

	assert.Equal(t, 0, loop.active())
	assert.Empty(t, loop.skippedNames())
	assert.Equal(t, 0, count("regrab_skipped_unsupported_type"))
	_, ok := m.unresolvedGauge("radarr")
	assert.False(t, ok)

	cancel()
	waitWG(t, &bgWG, 2*time.Second)
}
