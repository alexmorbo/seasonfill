package adapters_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	catalogseries "github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeProbe records IsStale calls and returns canned (stale, reason).
type fakeProbe struct {
	stale  bool
	reason string

	mu    sync.Mutex
	calls int
}

func (p *fakeProbe) IsStale(_ context.Context, _ domain.SeriesID, _ string) (bool, string) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.stale, p.reason
}

// fakeAsyncEnricher records EnqueueIfStale calls.
type fakeAsyncEnricher struct {
	mu    sync.Mutex
	calls int
}

func (e *fakeAsyncEnricher) EnqueueIfStale(_ domain.SeriesID, _ catalogseries.Hydration) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
}

func (e *fakeAsyncEnricher) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// fakeWorker satisfies SeriesWorkerHandle. Configurable: block until
// ctx.Done (timeout test), return canned error (error test), or noop+nil.
type fakeWorker struct {
	calls atomic.Int64

	block       bool
	err         error
	recordCtxAt atomic.Pointer[time.Time] // entry timestamp if needed

	// optional gate the test fires to release a Handle blocking with
	// runtime sync gate.
	releaseCh chan struct{}
}

func (f *fakeWorker) Handle(ctx context.Context, _ domain.SeriesID) error {
	f.calls.Add(1)
	now := time.Now()
	f.recordCtxAt.Store(&now)
	if f.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func newFreshener(t *testing.T, probe adapters.StalenessProbe, enr *fakeAsyncEnricher, timeout time.Duration) *adapters.SeriesFreshenerHolder {
	t.Helper()
	h, err := adapters.NewSeriesFreshenerHolder(adapters.SeriesFreshenerConfig{
		Probe:         probe,
		AsyncEnricher: enr,
		SyncTimeout:   timeout,
	})
	require.NoError(t, err)
	return h
}

func TestSeriesFreshenerHolder_RequiredFields(t *testing.T) {
	t.Parallel()
	_, err := adapters.NewSeriesFreshenerHolder(adapters.SeriesFreshenerConfig{})
	require.Error(t, err)
	_, err = adapters.NewSeriesFreshenerHolder(adapters.SeriesFreshenerConfig{Probe: &fakeProbe{}})
	require.Error(t, err)
}

func TestSeriesFreshenerHolder_FreshPath_NoWorkerCall(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: false, reason: "fresh"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "en-US")
	assert.True(t, res.Fresh)
	assert.False(t, res.Refreshed)
	assert.False(t, res.Degraded)
	assert.Equal(t, int64(0), w.calls.Load())
	assert.Equal(t, 0, enr.Calls())
}

func TestSeriesFreshenerHolder_StubStalePath_Refreshed(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "ru-RU")
	assert.True(t, res.Refreshed)
	assert.False(t, res.Degraded)
	assert.Equal(t, int64(1), w.calls.Load())
	assert.Equal(t, 0, enr.Calls(), "successful refresh does NOT enqueue async")
}

func TestSeriesFreshenerHolder_Singleflight_CoalescesSameKey(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	releaseCh := make(chan struct{})
	w := &fakeWorker{releaseCh: releaseCh}
	h.Set(w)
	defer h.Close()

	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([]bool, N)
	for i := range N {
		go func() {
			defer wg.Done()
			res := h.EnsureFresh(context.Background(), 99, "ru-RU")
			results[i] = res.Refreshed
		}()
	}
	// Allow goroutines to enter singleflight before releasing the worker.
	time.Sleep(30 * time.Millisecond)
	close(releaseCh)
	wg.Wait()

	assert.Equal(t, int64(1), w.calls.Load(), "singleflight must coalesce concurrent calls onto one Handle")
	// All callers should see Refreshed=true via shared result.
	for i, r := range results {
		assert.True(t, r, "caller %d expected Refreshed=true", i)
	}
}

func TestSeriesFreshenerHolder_Singleflight_DifferentLangsDoNotCoalesce(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	releaseCh := make(chan struct{})
	w := &fakeWorker{releaseCh: releaseCh}
	h.Set(w)
	defer h.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = h.EnsureFresh(context.Background(), 99, "ru-RU")
	}()
	go func() {
		defer wg.Done()
		_ = h.EnsureFresh(context.Background(), 99, "en-US")
	}()
	time.Sleep(30 * time.Millisecond)
	close(releaseCh)
	wg.Wait()

	assert.Equal(t, int64(2), w.calls.Load(), "different langs must NOT coalesce")
}

func TestSeriesFreshenerHolder_TimeoutPath_DegradedAndAsyncFallback(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, 50*time.Millisecond)
	w := &fakeWorker{block: true}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "ru-RU")
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(1), w.calls.Load())
	assert.Equal(t, 1, enr.Calls(), "timeout must enqueue async fallback exactly once")
}

func TestSeriesFreshenerHolder_ErrorPath_DegradedAndAsyncFallback(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "ttl"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{err: errors.New("boom")}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "en-US")
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(1), w.calls.Load())
	assert.Equal(t, 1, enr.Calls())
}

func TestSeriesFreshenerHolder_NoWorkerPath_AsyncOnly(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	// NOTE: never Set(...) — inner stays nil.
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "ru-RU")
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, 1, enr.Calls(), "no worker path must enqueue async fallback")
}

func TestSeriesFreshenerHolder_NegativeSeriesID_Skipped(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 0, "ru-RU")
	assert.True(t, res.Fresh)
	assert.Equal(t, int64(0), w.calls.Load())
	assert.Equal(t, 0, enr.Calls())
	probe.mu.Lock()
	assert.Equal(t, 0, probe.calls, "probe must NOT be called for invalid seriesID")
	probe.mu.Unlock()
}

func TestSeriesFreshenerHolder_ClosedHolder_Skipped(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	h.Close()

	res := h.EnsureFresh(context.Background(), 42, "ru-RU")
	assert.True(t, res.Fresh)
	assert.Equal(t, int64(0), w.calls.Load())
	assert.Equal(t, 0, enr.Calls())
}

func TestSeriesFreshenerHolder_DetachedCtx_SurvivesCallerCancel(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "stub"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)

	// Worker records ctx error at entry. Detached ctx must have err==nil.
	var entry atomic.Pointer[struct{ err error }]
	releaseCh := make(chan struct{})
	w := &workerCtxRecorder{releaseCh: releaseCh, entry: &entry}
	h.Set(w)
	defer h.Close()

	parent, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{}, 1)
	go func() {
		_ = h.EnsureFresh(parent, 42, "ru-RU")
		doneCh <- struct{}{}
	}()
	// Cancel the parent ctx immediately. Worker's detached ctx should
	// still see Err()=nil at entry.
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(releaseCh)
	<-doneCh

	state := entry.Load()
	require.NotNil(t, state)
	assert.NoError(t, state.err, "worker MUST observe detached ctx (Err()=nil at entry)")
	assert.Equal(t, int64(1), w.calls.Load())
}

// workerCtxRecorder is a fakeWorker variant that records ctx.Err() at
// entry — exposes the detached-ctx invariant under test.
type workerCtxRecorder struct {
	calls     atomic.Int64
	releaseCh chan struct{}
	entry     *atomic.Pointer[struct{ err error }]
}

func (f *workerCtxRecorder) Handle(ctx context.Context, _ domain.SeriesID) error {
	f.calls.Add(1)
	state := struct{ err error }{err: ctx.Err()}
	f.entry.Store(&state)
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
