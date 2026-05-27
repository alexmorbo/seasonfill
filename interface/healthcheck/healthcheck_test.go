package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type fakeSonarr struct {
	name string
	err  error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	if f.err != nil {
		return ports.SystemStatus{}, f.err
	}
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (f *fakeSonarr) Name() string { return f.name }

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func TestChecker_New_InitialStateUnknown(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "a"}, &fakeSonarr{name: "b"}})
	snap := c.Snapshot()
	assert.Len(t, snap, 2)
	for _, h := range snap {
		assert.Equal(t, instance.HealthUnavailableUnknown, h.Health)
	}
	assert.False(t, c.AnyInstanceAvailable())
}

func TestChecker_Preflight_AllUp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})
	c.Preflight(context.Background())

	assert.True(t, c.AnyInstanceAvailable())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthAvailable, snap[0].Health)
	assert.Empty(t, snap[0].LastError)
}

func TestChecker_Preflight_Auth(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{
		name: "main",
		err:  fmt.Errorf("%w: 401", domain.ErrInstanceUnauthorized),
	}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableAuth, snap[0].Health)
}

func TestChecker_Preflight_Network(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{
		name: "main",
		err:  fmt.Errorf("dial fail: %w", domain.ErrInstanceNetwork),
	}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableNetwork, snap[0].Health)
}

func TestChecker_Preflight_UnknownErr(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main", err: errors.New("???")}})
	c.Preflight(context.Background())
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthUnavailableUnknown, snap[0].Health)
}

func TestChecker_Preflight_Mixed_AnyAvailable(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{
		&fakeSonarr{name: "ok"},
		&fakeSonarr{name: "fail", err: errors.New("nope")},
	})
	c.Preflight(context.Background())
	assert.True(t, c.AnyInstanceAvailable(), "any available is enough")
}

func TestChecker_RecheckByName(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	a := &fakeSonarr{name: "a", err: errors.New("boom")}
	c := New(db, []ports.SonarrClient{a})
	c.Preflight(context.Background())
	a.err = nil
	c.RecheckByName(context.Background(), "a")
	snap := c.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, instance.HealthAvailable, snap[0].Health)
}

func TestChecker_RecheckByName_UnknownInstanceIsNoOp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "a"}})
	c.Preflight(context.Background())
	c.RecheckByName(context.Background(), "missing")
	snap := c.Snapshot()
	require.Len(t, snap, 1)
}

func TestChecker_DatabaseUp(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, nil)
	assert.True(t, c.DatabaseUp(context.Background()))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	assert.False(t, c.DatabaseUp(context.Background()))
}

func TestChecker_Registry_Exposed(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})
	require.NotNil(t, c.Registry())
	assert.Len(t, c.Registry().Names(), 1)
}

func TestChecker_Run_StopsOnContextCancel(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "main"}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx, 50*time.Millisecond)
		close(done)
	}()

	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	assert.True(t, c.AnyInstanceAvailable())
}

func TestChecker_ReplaceClients_PreservesRegistryPointer(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	regBefore := c.Registry()

	c.ReplaceClients(
		[]ports.SonarrClient{&fakeSonarr{name: "alpha"}, &fakeSonarr{name: "beta"}},
		[]string{"alpha", "beta"},
	)
	regAfter := c.Registry()

	assert.Same(t, regBefore, regAfter,
		"ReplaceClients must NOT reassign the registry pointer — watchdog + scan UC hold it forever")
}

func TestChecker_ReplaceClients_NamesPropagateToRegistry(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "alpha"}})

	c.ReplaceClients(
		[]ports.SonarrClient{&fakeSonarr{name: "alpha"}, &fakeSonarr{name: "beta"}},
		[]string{"alpha", "beta"},
	)
	names := c.Registry().Names()
	assert.ElementsMatch(t, []string{"alpha", "beta"}, names)

	// Remove alpha.
	c.ReplaceClients(
		[]ports.SonarrClient{&fakeSonarr{name: "beta"}},
		[]string{"beta"},
	)
	names = c.Registry().Names()
	assert.ElementsMatch(t, []string{"beta"}, names)
	_, ok := c.Registry().Get("alpha")
	assert.False(t, ok, "removed instance must drop from registry")
}

func TestChecker_ReplaceClients_AtomicSwapWithConcurrentReader(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "alpha"}})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader goroutine: iterate the (atomic-pointer) client list via
	// Preflight. Any non-atomic access would trip -race here.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.Preflight(context.Background())
			}
		}
	}()

	for i := 0; i < 200; i++ {
		clients := []ports.SonarrClient{
			&fakeSonarr{name: "alpha"},
			&fakeSonarr{name: "beta"},
		}
		c.ReplaceClients(clients, []string{"alpha", "beta"})
		c.ReplaceClients(
			[]ports.SonarrClient{&fakeSonarr{name: "alpha"}},
			[]string{"alpha"},
		)
	}
	close(stop)
	wg.Wait()
}

func TestChecker_ReplaceClients_RaceWithMarkAvailable(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "alpha"}})

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Goroutine 1: hammer the registry directly (simulates checkOne
	// firing concurrently with reload).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				c.Registry().MarkAvailable("alpha", time.Now().UTC())
			}
		}
	}()

	// Goroutine 2: reload at full speed.
	for i := 0; i < 200; i++ {
		c.ReplaceClients(
			[]ports.SonarrClient{&fakeSonarr{name: "alpha"}, &fakeSonarr{name: "beta"}},
			[]string{"alpha", "beta"},
		)
		c.ReplaceClients(
			[]ports.SonarrClient{&fakeSonarr{name: "alpha"}},
			[]string{"alpha"},
		)
	}
	close(stop)
	wg.Wait()

	// alpha must still be in the registry; final SetNames pruned beta.
	assert.Contains(t, c.Registry().Names(), "alpha")
	assert.NotContains(t, c.Registry().Names(), "beta")
}

func TestChecker_ReplaceClients_EmptyClientsEmptiesRegistry(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, []ports.SonarrClient{&fakeSonarr{name: "alpha"}})

	c.ReplaceClients(nil, nil)

	assert.Empty(t, c.Registry().Names())
	// Preflight on empty slice must not panic.
	c.Preflight(context.Background())
}

// slowFakeSonarr blocks SystemStatus until release() is called, while
// counting probe invocations atomically. Used to assert single-flight.
type slowFakeSonarr struct {
	name    string
	probes  atomic.Int64
	release chan struct{}
}

func (s *slowFakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	s.probes.Add(1)
	<-s.release
	return ports.SystemStatus{Version: "test"}, nil
}
func (s *slowFakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (s *slowFakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (s *slowFakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (s *slowFakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (s *slowFakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (s *slowFakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (s *slowFakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) {
	return nil, nil
}
func (s *slowFakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error) { return nil, nil }
func (s *slowFakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (s *slowFakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (s *slowFakeSonarr) Name() string { return s.name }

func TestChecker_Preflight_SingleFlight(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	slow := &slowFakeSonarr{name: "main", release: make(chan struct{})}
	c := New(db, []ports.SonarrClient{slow})

	const callers = 5
	var wg sync.WaitGroup
	started := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			c.Preflight(context.Background())
		}()
	}
	for i := 0; i < callers; i++ {
		<-started
	}

	// Give every caller a chance to enter Preflight. The winner is
	// blocked on slow.release; losers must early-return immediately.
	require.Eventually(t, func() bool {
		return slow.probes.Load() == 1
	}, time.Second, 5*time.Millisecond,
		"exactly one probe must be in flight while gate is held")

	close(slow.release)
	wg.Wait()

	assert.Equal(t, int64(1), slow.probes.Load(),
		"single-flight gate must coalesce concurrent Preflight calls to one probe round")

	// After the gate releases, a fresh Preflight must run normally.
	slow.release = make(chan struct{})
	close(slow.release)
	c.Preflight(context.Background())
	assert.Equal(t, int64(2), slow.probes.Load(),
		"new Preflight after gate clears must execute")
}

// sleepyFakeSonarr blocks SystemStatus for a fixed duration, counting
// the peak number of concurrent in-flight probes. Used to assert
// bounded-parallel Preflight.
type sleepyFakeSonarr struct {
	name    string
	delay   time.Duration
	inFlight *atomic.Int64
	peak     *atomic.Int64
}

func (s *sleepyFakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	now := s.inFlight.Add(1)
	for {
		p := s.peak.Load()
		if now <= p || s.peak.CompareAndSwap(p, now) {
			break
		}
	}
	time.Sleep(s.delay)
	s.inFlight.Add(-1)
	return ports.SystemStatus{Version: "test"}, nil
}
func (s *sleepyFakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (s *sleepyFakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (s *sleepyFakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (s *sleepyFakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (s *sleepyFakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (s *sleepyFakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (s *sleepyFakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) {
	return nil, nil
}
func (s *sleepyFakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error) { return nil, nil }
func (s *sleepyFakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (s *sleepyFakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (s *sleepyFakeSonarr) Name() string { return s.name }

func TestChecker_Preflight_BoundedParallel(t *testing.T) {
	t.Parallel()
	db := openDB(t)

	const (
		instances = 8
		limit     = 4
		delay     = 100 * time.Millisecond
	)
	var inFlight, peak atomic.Int64
	clients := make([]ports.SonarrClient, 0, instances)
	for i := 0; i < instances; i++ {
		clients = append(clients, &sleepyFakeSonarr{
			name:     fmt.Sprintf("inst-%d", i),
			delay:    delay,
			inFlight: &inFlight,
			peak:     &peak,
		})
	}
	c := New(db, clients)

	start := time.Now()
	c.Preflight(context.Background())
	elapsed := time.Since(start)

	// Sequential lower bound would be instances*delay = 800ms.
	// Parallel-4 expected ~ceil(8/4)*100ms = 200ms; allow generous
	// CI slack (cap at 500ms) — still proves it's not sequential.
	assert.Less(t, elapsed, 500*time.Millisecond,
		"bounded-parallel Preflight must finish well under the sequential lower bound of %v", instances*delay)
	assert.GreaterOrEqual(t, elapsed, 2*delay,
		"with limit=%d and %d instances, at least ceil(%d/%d)=2 batches must run", limit, instances, instances, limit)
	assert.LessOrEqual(t, peak.Load(), int64(limit),
		"peak in-flight probes must not exceed the configured limit")
	assert.Greater(t, peak.Load(), int64(1),
		"peak in-flight probes must exceed 1 — otherwise Preflight is still sequential")

	// Every instance must have been marked available.
	for _, snap := range c.Snapshot() {
		assert.Equal(t, instance.HealthAvailable, snap.Health, "instance %s not marked available", snap.Name)
	}
}

func TestChecker_New_InstancesPointerNeverNil(t *testing.T) {
	t.Parallel()
	db := openDB(t)
	c := New(db, nil)
	// Preflight + RecheckByName must work on a freshly-constructed
	// Checker that received no clients (was the regression target
	// when migrating to atomic.Pointer).
	c.Preflight(context.Background())
	c.RecheckByName(context.Background(), "missing")
	assert.Empty(t, c.Snapshot())
}
