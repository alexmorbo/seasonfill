package adapters_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeDispatcher counts enqueues; Close is a no-op.
type fakeDispatcher struct {
	count atomic.Int32
}

func (f *fakeDispatcher) Enqueue(_ appenrich.EntityKind, _ int64, _ appenrich.Priority) {
	f.count.Add(1)
}
func (f *fakeDispatcher) Close() {}

func TestOnDemandEnricherHolder_NilDispatcherIsNoop(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	// No panic; no enqueue (no dispatcher Set).
	h.EnqueueIfStale(domain.SeriesID(42), series.HydrationStub)
	// Give the goroutine a moment so a regression that does call inner
	// would show up.
	time.Sleep(20 * time.Millisecond)
}

func TestOnDemandEnricherHolder_FullSkips(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	h.EnqueueIfStale(domain.SeriesID(42), series.HydrationFull)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(0), d.count.Load(), "Full hydration must skip enqueue")
}

func TestOnDemandEnricherHolder_StubEnqueues(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	h.EnqueueIfStale(domain.SeriesID(42), series.HydrationStub)
	require.Eventually(t, func() bool {
		return d.count.Load() == 1
	}, 200*time.Millisecond, 5*time.Millisecond, "stub must enqueue once")
}

func TestOnDemandEnricherHolder_ThrottleDedupsWithinWindow(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	// 5 immediate calls — only the first should land an enqueue.
	for range 5 {
		h.EnqueueIfStale(domain.SeriesID(42), series.HydrationStub)
	}
	require.Eventually(t, func() bool {
		return d.count.Load() >= 1
	}, 200*time.Millisecond, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond) // settle
	assert.Equal(t, int32(1), d.count.Load(),
		"throttle must dedupe within 30s window")
}

func TestOnDemandEnricherHolder_DifferentIDsBothEnqueue(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	h.EnqueueIfStale(domain.SeriesID(1), series.HydrationStub)
	h.EnqueueIfStale(domain.SeriesID(2), series.HydrationStub)
	require.Eventually(t, func() bool {
		return d.count.Load() == 2
	}, 200*time.Millisecond, 5*time.Millisecond, "distinct ids must enqueue independently")
}

func TestOnDemandEnricherHolder_InvalidIDSkips(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	h.EnqueueIfStale(domain.SeriesID(0), series.HydrationStub)
	h.EnqueueIfStale(domain.SeriesID(-1), series.HydrationStub)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(0), d.count.Load())
}

func TestOnDemandEnricherHolder_CloseStopsSweep(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	h.Close()
	h.Close() // idempotent — second Close must not panic
}

func TestOnDemandEnricherHolder_RaceSafeConcurrentEnqueue(t *testing.T) {
	t.Parallel()
	h := adapters.NewOnDemandEnricherHolder(nil)
	defer h.Close()
	d := &fakeDispatcher{}
	h.Set(d)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			h.EnqueueIfStale(domain.SeriesID(int64(id)+1), series.HydrationStub)
		}(i)
	}
	wg.Wait()
	require.Eventually(t, func() bool {
		return d.count.Load() == 50
	}, 500*time.Millisecond, 5*time.Millisecond,
		"50 distinct ids must each enqueue exactly once (run with -race)")
}
