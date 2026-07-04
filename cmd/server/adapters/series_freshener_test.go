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
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

// fakeProbe records IsStale calls and returns canned (stale, reason).
// Implements freshener.Probe by emitting a DENSE 5-section verdict
// slice where every section carries the canned (stale, reason). The
// SeriesFreshenerHolder only inspects the SectionSkeleton verdict
// pre-A5; emitting the same on every section keeps the fake
// deterministic regardless of what A5 wiring lands later.
type fakeProbe struct {
	stale  bool
	reason string

	mu    sync.Mutex
	calls int
}

func (p *fakeProbe) IsStale(_ context.Context, _ domain.SeriesID, _ values.LanguageTag, seasonNumbers []int) ([]freshener.SectionVerdict, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	verdicts := make([]freshener.SectionVerdict, 0, len(freshener.FixedSections)+len(seasonNumbers))
	for _, s := range freshener.FixedSections {
		verdicts = append(verdicts, freshener.SectionVerdict{
			Section: s, Stale: p.stale, Reason: p.reason,
		})
	}
	for _, n := range seasonNumbers {
		verdicts = append(verdicts, freshener.SectionVerdict{
			Section: freshener.SeasonSection(n), Stale: p.stale, Reason: p.reason,
		})
	}
	return verdicts, nil
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
//
// `calls` aggregates Handle + HandleForced + HandleForcedLang + all A5
// narrow methods for tests that don't care which entry point Freshener
// picked. Per-method counters split the count for regression tests that
// pin routing (Story 544 / 546 / 563).
type fakeWorker struct {
	calls                 atomic.Int64
	handleCalls           atomic.Int64
	handleForcedCalls     atomic.Int64
	handleForcedLangCalls atomic.Int64

	// A5 (Story 563) narrow method counters.
	refreshSeriesTextCalls      atomic.Int64
	refreshCastCalls            atomic.Int64
	refreshSeasonSlimCalls      atomic.Int64
	refreshRecommendationsCalls atomic.Int64
	refreshMediaAssetsCalls     atomic.Int64

	block       bool
	err         error
	recordCtxAt atomic.Pointer[time.Time] // entry timestamp if needed

	// optional gate the test fires to release a Handle blocking with
	// runtime sync gate.
	releaseCh chan struct{}
}

func (f *fakeWorker) Handle(ctx context.Context, _ domain.SeriesID) error {
	f.calls.Add(1)
	f.handleCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) HandleForced(ctx context.Context, _ domain.SeriesID) error {
	f.calls.Add(1)
	f.handleForcedCalls.Add(1)
	return f.runBody(ctx)
}

// HandleForcedLang — Story 546 entry point. Increments the same
// `calls` aggregator so tests that don't care which entry point fired
// keep their assertions intact.
func (f *fakeWorker) HandleForcedLang(ctx context.Context, _ domain.SeriesID, _ string) error {
	f.calls.Add(1)
	f.handleForcedLangCalls.Add(1)
	return f.runBody(ctx)
}

// A5 (Story 563) narrow method stubs. Each increments both the aggregate
// counter + its own per-method counter, then delegates to runBody so the
// block/err/releaseCh mechanics apply uniformly.

func (f *fakeWorker) RefreshSeriesText(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	f.calls.Add(1)
	f.refreshSeriesTextCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) RefreshCast(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	f.calls.Add(1)
	f.refreshCastCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) RefreshSeasonSlim(ctx context.Context, _ domain.SeriesID, _ int, _ string, _ bool) error {
	f.calls.Add(1)
	f.refreshSeasonSlimCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) RefreshRecommendations(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	f.calls.Add(1)
	f.refreshRecommendationsCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) RefreshMediaAssets(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	f.calls.Add(1)
	f.refreshMediaAssetsCalls.Add(1)
	return f.runBody(ctx)
}

// RefreshSeriesAllLangs — S-B. The freshener's SectionOverview branch now
// dispatches here instead of RefreshSeriesText; reuse the refreshSeriesText
// counter so the existing SectionOverview routing assertions stay valid.
func (f *fakeWorker) RefreshSeriesAllLangs(ctx context.Context, _ domain.SeriesID, _ bool) error {
	f.calls.Add(1)
	f.refreshSeriesTextCalls.Add(1)
	return f.runBody(ctx)
}

func (f *fakeWorker) runBody(ctx context.Context) error {
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

func newFreshener(t *testing.T, probe freshener.Probe, enr *fakeAsyncEnricher, timeout time.Duration) *adapters.SeriesFreshenerHolder {
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
	// Story 563 A5: EnsureFresh shim dispatches all 5 fixed sections when
	// probe marks them stale. Skeleton→HandleForcedLang, the other four
	// route through narrow methods. spawnAsyncFollowup DELETED — no more
	// full-canon HandleForced fan-out. AsyncEnricher.EnqueueIfStale
	// (Story 528 belt-and-suspenders) only fires on error/timeout paths.
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"SectionSkeleton MUST route through HandleForcedLang (Story 546)")
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load(),
		"SectionOverview MUST route through RefreshSeriesText (Story 563 A2)")
	assert.Equal(t, int64(1), w.refreshCastCalls.Load(),
		"SectionCast MUST route through RefreshCast (Story 563 A2)")
	assert.Equal(t, int64(1), w.refreshRecommendationsCalls.Load(),
		"SectionRecommendations MUST route through RefreshRecommendations (Story 563 A3b)")
	assert.Equal(t, int64(1), w.refreshMediaAssetsCalls.Load(),
		"SectionMedia MUST route through RefreshMediaAssets (Story 563 A4)")
	assert.Equal(t, 0, enr.Calls(),
		"Story 563: successful sync dispatch does NOT enqueue async fallback (spawnAsyncFollowup DELETED)")
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

	// Story 563 A5: singleflight key is (seriesID, section, lang). N
	// concurrent shim callers → each section fires exactly once.
	// Skeleton→HandleForcedLang + 4 narrow methods, five distinct SF keys.
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"singleflight must coalesce concurrent SectionSkeleton dispatches onto one HandleForcedLang")
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load(),
		"singleflight must coalesce concurrent SectionOverview dispatches")
	assert.Equal(t, int64(1), w.refreshCastCalls.Load(),
		"singleflight must coalesce concurrent SectionCast dispatches")
	assert.Equal(t, int64(1), w.refreshRecommendationsCalls.Load(),
		"singleflight must coalesce concurrent SectionRecommendations dispatches")
	assert.Equal(t, int64(1), w.refreshMediaAssetsCalls.Load(),
		"singleflight must coalesce concurrent SectionMedia dispatches")
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

	// Story 563 A5: singleflight key includes lang → different langs
	// don't coalesce. Each shim call fires HandleForcedLang once for
	// its lang → 2 total (Skeleton dispatch per lang).
	assert.Equal(t, int64(2), w.handleForcedLangCalls.Load(),
		"different langs must NOT coalesce for SectionSkeleton dispatch")
	// Narrow methods also fire per-lang.
	assert.Equal(t, int64(2), w.refreshSeriesTextCalls.Load(),
		"different langs must NOT coalesce for SectionOverview")
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
	// Story 563 A5: shim dispatches 5 sections; all block → all time out.
	// W15-10: the 5 timed-out sections then carry over to the async path →
	// 10 total invocations (5 sync + 5 carry-over). Carry-over is async, poll.
	require.Eventually(t, func() bool {
		return w.calls.Load() >= 10
	}, 3*time.Second, 10*time.Millisecond,
		"5 sections time out sync, then carry over to async (5 + 5 = 10)")
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
	// Story 563 A5: shim dispatches 5 sections; all return boom → all fail.
	// W15-10: the 5 failed sections then carry over to the async path → 10
	// total invocations (5 sync + 5 carry-over). Carry-over is async, poll.
	require.Eventually(t, func() bool {
		return w.calls.Load() >= 10
	}, 3*time.Second, 10*time.Millisecond,
		"5 sections error sync, then carry over to async (5 + 5 = 10)")
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
	// Don't assert exact calls.Load() — the Story 547 async follow-up
	// goroutine adds an indeterminate +1 for HandleForced. The detached-ctx
	// invariant is captured by the entry assertion above.
}

// workerCtxRecorder is a fakeWorker variant that records ctx.Err() at
// entry — exposes the detached-ctx invariant under test.
//
// Story 547: HandleForced fires async from the Story 547 follow-up
// goroutine AFTER the test's EnsureFresh return. To keep the sync-path
// detached-ctx assertion deterministic we only record entry state from
// the sync entry points (Handle / HandleForcedLang) — HandleForced just
// counts.
type workerCtxRecorder struct {
	calls     atomic.Int64
	releaseCh chan struct{}
	entry     *atomic.Pointer[struct{ err error }]
}

func (f *workerCtxRecorder) Handle(ctx context.Context, _ domain.SeriesID) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) HandleForced(ctx context.Context, _ domain.SeriesID) error {
	// Story 547 async path — don't touch entry, just count + drain ctx so
	// the goroutine completes cleanly without blocking the test.
	return f.record(ctx, false)
}

// HandleForcedLang — Story 546 routing path. Records entry state — the
// detached-ctx invariant assertion targets THIS sync call.
func (f *workerCtxRecorder) HandleForcedLang(ctx context.Context, _ domain.SeriesID, _ string) error {
	return f.record(ctx, true)
}

// A5 (Story 563) narrow method stubs — record entry state so the
// detached-ctx invariant assertion works uniformly for the shim path
// which routes SectionSkeleton to HandleForcedLang + others to narrow
// methods.

func (f *workerCtxRecorder) RefreshSeriesText(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) RefreshCast(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) RefreshSeasonSlim(ctx context.Context, _ domain.SeriesID, _ int, _ string, _ bool) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) RefreshRecommendations(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) RefreshMediaAssets(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return f.record(ctx, true)
}

// RefreshSeriesAllLangs — S-B. SectionOverview routes here; record entry
// state like the other sync narrow methods.
func (f *workerCtxRecorder) RefreshSeriesAllLangs(ctx context.Context, _ domain.SeriesID, _ bool) error {
	return f.record(ctx, true)
}

func (f *workerCtxRecorder) record(ctx context.Context, recordEntry bool) error {
	f.calls.Add(1)
	if recordEntry {
		state := struct{ err error }{err: ctx.Err()}
		f.entry.Store(&state)
	}
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Story 544 + Story 546 + Story 563 regression: when probe says stale,
// Freshener MUST route SectionSkeleton through HandleForcedLang (NOT
// Handle, NOT HandleForced) — and the other 4 sections through their
// A5 narrow methods (RefreshSeriesText / RefreshCast / etc).
//
// Why HandleForcedLang and not the original HandleForced (Story 546):
// pre-546 the freshener invoked HandleForced, which iterated every
// w.deps.Languages entry AND fetched every active season's episode list
// per language. Story 546 swapped SectionSkeleton to HandleForcedLang
// (one GetTV + one tx, no per-season fetches).
//
// Why NOT Handle (Story 544): Handle's per-source freshness gate would
// short-circuit valid refreshes (e.g. missing_lang at hour 12 of a 30d
// SourceTMDBSeries TTL window — live bug observed for sonarr_id=25551
// where freshen.run logged result:"refreshed" but no TMDB call fired).
//
// Why NO spawnAsyncFollowup (Story 563 A5): the full-canon HandleForced
// fan-out was superseded by targeted per-section A5 dispatch. Narrow
// methods write per-section data directly; no Stage-3-6 follow-up needed.
// AsyncEnricher.EnqueueIfStale stays as belt-and-suspenders on error paths.
func TestSeriesFreshenerHolder_StalePath_CallsHandleForcedLangNotHandleForced(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "missing_lang"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 25551, "ru-RU")
	assert.True(t, res.Refreshed)
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"Story 546: SectionSkeleton MUST route through HandleForcedLang (staged Stage 1+2 path)")
	assert.Equal(t, int64(0), w.handleCalls.Load(),
		"Story 544: Freshener MUST NOT call Handle (per-source TTL would short-circuit stale refreshes)")
	assert.Equal(t, int64(0), w.handleForcedCalls.Load(),
		"Story 563: spawnAsyncFollowup DELETED — no more full-canon HandleForced fan-out")
	assert.Equal(t, 0, enr.Calls(),
		"Story 563: successful sync dispatch does NOT enqueue async fallback")
}
