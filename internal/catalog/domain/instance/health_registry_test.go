package instance

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureListener struct {
	mu     sync.Mutex
	checks int
	trans  []struct{ from, to Health }
}

func (c *captureListener) OnCheck(_ string, _ Health, _ time.Time) {
	c.mu.Lock()
	c.checks++
	c.mu.Unlock()
}

func (c *captureListener) OnTransition(_ string, from, to Health, _ time.Time, _ string) {
	c.mu.Lock()
	c.trans = append(c.trans, struct{ from, to Health }{from, to})
	c.mu.Unlock()
}

func TestRegistry_NewSeedsBootstrapping(t *testing.T) {
	t.Parallel()
	// Story 488 (B-14): fresh entries seed in Bootstrapping until the
	// first preflight transitions them out.
	r := NewRegistry([]string{"a", "b"})
	for _, name := range []string{"a", "b"} {
		s, ok := r.Get(name)
		require.True(t, ok)
		assert.Equal(t, HealthBootstrapping, s.Health)
	}
}

func TestRegistry_MarkAvailable_Transition(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	listener := &captureListener{}
	r.WithListener(listener)
	from, changed := r.MarkAvailable("a", time.Now().UTC())
	// Story 488 (B-14): fresh seed is Bootstrapping, not Unknown.
	assert.Equal(t, HealthBootstrapping, from)
	assert.True(t, changed)
	s, _ := r.Get("a")
	assert.Equal(t, HealthAvailable, s.Health)
	assert.Equal(t, 1, s.TransitionsCount)
	assert.True(t, r.AnyAvailable())
	listener.mu.Lock()
	defer listener.mu.Unlock()
	assert.Equal(t, 1, listener.checks)
	assert.Len(t, listener.trans, 1)
}

func TestRegistry_MarkAvailable_NoChangeStillFiresCheck(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	r.MarkAvailable("a", time.Now().UTC())
	listener := &captureListener{}
	r.WithListener(listener)
	_, changed := r.MarkAvailable("a", time.Now().UTC())
	assert.False(t, changed)
	listener.mu.Lock()
	defer listener.mu.Unlock()
	assert.Equal(t, 1, listener.checks)
	assert.Empty(t, listener.trans)
}

func TestRegistry_MarkUnavailable_Variants(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	r.MarkAvailable("a", time.Now().UTC())
	r.MarkUnavailable("a", HealthUnavailableAuth, "unauthorized", time.Now().UTC())
	s, _ := r.Get("a")
	assert.Equal(t, HealthUnavailableAuth, s.Health)
	assert.Equal(t, "unauthorized", s.LastError)

	r.MarkUnavailable("a", HealthUnavailableNetwork, "dns", time.Now().UTC())
	s, _ = r.Get("a")
	assert.Equal(t, HealthUnavailableNetwork, s.Health)

	// HealthAvailable coerced to HealthUnavailableUnknown.
	r.MarkUnavailable("a", HealthAvailable, "x", time.Now().UTC())
	s, _ = r.Get("a")
	assert.Equal(t, HealthUnavailableUnknown, s.Health)
}

func TestRegistry_MarkUnavailable_TracksTransitionCount(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	// 1: Bootstrapping -> Available (Story 488 — seed is Bootstrapping)
	r.MarkAvailable("a", time.Now().UTC())
	// 2: Available -> Auth
	r.MarkUnavailable("a", HealthUnavailableAuth, "x", time.Now().UTC())
	// no-op
	r.MarkUnavailable("a", HealthUnavailableAuth, "x", time.Now().UTC())
	// 3: Auth -> Available
	r.MarkAvailable("a", time.Now().UTC())
	s, _ := r.Get("a")
	assert.Equal(t, 3, s.TransitionsCount)
}

func TestRegistry_Snapshot_StableCopy(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a", "b"})
	snap := r.Snapshot()
	assert.Len(t, snap, 2)
}

func TestRegistry_Concurrency_NoRace(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); r.MarkAvailable("a", time.Now().UTC()) }()
		go func() {
			defer wg.Done()
			r.MarkUnavailable("a", HealthUnavailableNetwork, "", time.Now().UTC())
		}()
	}
	wg.Wait()
	_, ok := r.Get("a")
	assert.True(t, ok)
}

func TestRegistry_Names(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a", "b"})
	names := r.Names()
	assert.Len(t, names, 2)
}

func TestRegistry_Get_Unknown(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	_, ok := r.Get("missing")
	assert.False(t, ok)
}

func TestRegistry_SetNames_AddsAndRemoves(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a", "b"})
	added, removed := r.SetNames([]string{"b", "c"})

	assert.ElementsMatch(t, []string{"c"}, added)
	assert.ElementsMatch(t, []string{"a"}, removed)

	names := r.Names()
	assert.ElementsMatch(t, []string{"b", "c"}, names)

	_, ok := r.Get("a")
	assert.False(t, ok, "removed name must not be reachable via Get")

	s, ok := r.Get("c")
	require.True(t, ok)
	// Story 488 (B-14): newly added name seeds in Bootstrapping.
	assert.Equal(t, HealthBootstrapping, s.Health,
		"newly added name must seed in Bootstrapping state")
}

func TestRegistry_SetNames_NoOpOnUnchanged(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a", "b"})
	r.MarkAvailable("a", time.Now().UTC())
	added, removed := r.SetNames([]string{"a", "b"})
	assert.Empty(t, added)
	assert.Empty(t, removed)

	// State of "a" must be preserved (still Available).
	s, _ := r.Get("a")
	assert.Equal(t, HealthAvailable, s.Health)
}

func TestRegistry_SetNames_EmptyTargetClears(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a", "b"})
	added, removed := r.SetNames(nil)
	assert.Empty(t, added)
	assert.ElementsMatch(t, []string{"a", "b"}, removed)
	assert.Empty(t, r.Names())
}

func TestRegistry_SetNames_PreservesUnchangedEntryState(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	r.MarkUnavailable("a", HealthUnavailableAuth, "401", time.Now().UTC())

	r.SetNames([]string{"a", "b"}) // add b, keep a
	s, _ := r.Get("a")
	assert.Equal(t, HealthUnavailableAuth, s.Health,
		"existing entry must not be reset by SetNames")
	assert.Equal(t, "401", s.LastError)
}

func TestRegistry_MarkAvailable_NoResurrectAfterRemove(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	r.SetNames([]string{}) // remove "a"
	_, changed := r.MarkAvailable("a", time.Now().UTC())
	assert.False(t, changed)
	_, ok := r.Get("a")
	assert.False(t, ok, "MarkAvailable must not resurrect a name removed from membership")
}

func TestRegistry_MarkUnavailable_NoResurrectAfterRemove(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})
	r.SetNames([]string{}) // remove "a"
	_, changed := r.MarkUnavailable("a", HealthUnavailableNetwork, "x", time.Now().UTC())
	assert.False(t, changed)
	_, ok := r.Get("a")
	assert.False(t, ok, "MarkUnavailable must not resurrect a name removed from membership")
}

// TestRegistry_NewRegistry_SeedsBootstrapping verifies Story 488 (B-14)
// seed behaviour at the constructor: a fresh registry returns entries
// in HealthBootstrapping rather than HealthUnavailableUnknown.
func TestRegistry_NewRegistry_SeedsBootstrapping(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"alpha"})
	snap, ok := r.Get("alpha")
	require.True(t, ok, "expected entry to be seeded")
	assert.Equal(t, HealthBootstrapping, snap.Health)
}

// TestRegistry_SetNames_AddsBootstrapping verifies Story 488 (B-14)
// seed behaviour at the reconcile path: entries added via SetNames also
// land in HealthBootstrapping.
func TestRegistry_SetNames_AddsBootstrapping(t *testing.T) {
	t.Parallel()
	r := NewRegistry(nil)
	added, _ := r.SetNames([]string{"beta"})
	require.Len(t, added, 1)
	assert.Equal(t, "beta", added[0])
	snap, ok := r.Get("beta")
	require.True(t, ok)
	assert.Equal(t, HealthBootstrapping, snap.Health)
}

// TestRegistry_Transition_OutOfBootstrapping verifies that the first
// preflight transitions a freshly-seeded entry to Available, bumps the
// transitions counter, and reports the previous state correctly.
func TestRegistry_Transition_OutOfBootstrapping(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"gamma"})
	now := time.Now().UTC()
	from, changed := r.MarkAvailable("gamma", now)
	assert.True(t, changed,
		"expected transition Bootstrapping → Available to be reported as changed")
	assert.Equal(t, HealthBootstrapping, from)
	snap, _ := r.Get("gamma")
	assert.Equal(t, HealthAvailable, snap.Health)
	assert.Equal(t, 1, snap.TransitionsCount)
}

func TestRegistry_SetNames_RaceWithMarkAvailable(t *testing.T) {
	t.Parallel()
	r := NewRegistry([]string{"a"})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				r.MarkAvailable("a", time.Now().UTC())
				r.MarkUnavailable("a", HealthUnavailableNetwork, "x", time.Now().UTC())
			}
		}
	})

	for range 100 {
		r.SetNames([]string{"a", "b"})
		r.SetNames([]string{"a"})
	}
	close(stop)
	wg.Wait()

	// Final state: "a" still present, "b" removed by last SetNames.
	names := r.Names()
	assert.Contains(t, names, "a")
	assert.NotContains(t, names, "b")
}
