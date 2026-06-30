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
// `calls` aggregates Handle + HandleForced + HandleForcedLang for tests
// that don't care which entry point Freshener picked. `handleCalls` /
// `handleForcedCalls` / `handleForcedLangCalls` split the count for the
// Story 544 + Story 546 regression tests that pin routing.
type fakeWorker struct {
	calls                 atomic.Int64
	handleCalls           atomic.Int64
	handleForcedCalls     atomic.Int64
	handleForcedLangCalls atomic.Int64

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
	// Sync path: exactly one HandleForcedLang (Stage 1+2). The Story 547
	// async HandleForced follow-up bumps w.calls.Load() to 2 eventually —
	// asserting the sync count specifically keeps this test deterministic
	// without sleeps.
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load())
	assert.Equal(t, 1, enr.Calls(),
		"Story 546: successful Stage 1+2 refresh enqueues async follow-up for episodes")
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

	// Sync path: one HandleForcedLang despite N concurrent callers. Story 547
	// async HandleForced bumps total calls eventually; assert the sync count
	// explicitly to keep deterministic.
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(), "singleflight must coalesce concurrent calls onto one HandleForcedLang")
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

	// Sync path: one HandleForcedLang per lang. Story 547 async HandleForced
	// bumps total calls eventually.
	assert.Equal(t, int64(2), w.handleForcedLangCalls.Load(), "different langs must NOT coalesce")
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

// Story 544 + Story 546 regression: when probe says stale, Freshener
// MUST route through HandleForcedLang (NOT Handle, NOT HandleForced).
//
// Why HandleForcedLang and not the original HandleForced (Story 546):
// pre-546 the freshener invoked HandleForced, which iterated every
// w.deps.Languages entry AND fetched every active season's episode list
// per language. On a 9-season series this consistently blew the 3s
// sync budget on lang #2 and rolled back the entire ru-RU tx — no
// series_texts.ru-RU row written, 2h backoff blocked retry. Story 546
// swapped the call to HandleForcedLang (one GetTV + one tx, no
// per-season fetches) and added a success-branch
// AsyncEnricher.EnqueueIfStale to schedule the background pass that
// fills episodes.
//
// Why NOT Handle (Story 544): Handle's per-source freshness gate would
// short-circuit valid refreshes (e.g. missing_lang at hour 12 of a 30d
// SourceTMDBSeries TTL window — live bug observed for sonarr_id=25551
// where freshen.run logged result:"refreshed" but no TMDB call fired).
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
		"Story 546: Freshener MUST route SYNC through HandleForcedLang (staged Stage 1+2 path)")
	assert.Equal(t, int64(0), w.handleCalls.Load(),
		"Story 544: Freshener MUST NOT call Handle (per-source TTL would short-circuit stale refreshes)")
	// HandleForced is INVOKED async by Story 547 (TTL-bypassing follow-up).
	// We don't pin its count here — see TestSeriesFreshenerHolder_StalePath_SpawnsHandleForcedAsync
	// for that assertion.
	assert.Equal(t, 1, enr.Calls(),
		"Story 546: successful Stage 1+2 enqueues async follow-up for episodes (belt-and-suspenders)")
}

// Story 547: when probe says stale, Freshener MUST spawn an async
// goroutine calling inner.HandleForced(detachedCtx, seriesID) so the
// follow-up bypasses the per-source TTL gate that Stage 1+2 just
// stamped via enrichment_tmdb_synced_at. Pre-547 the follow-up routed
// through the dispatcher → SeriesWorker.Handle which short-circuited
// with `enrichment.series.handle.fresh_skip` (live evidence
// sha-c9599b5 series 25551 — episode_texts.ru-RU stayed at 0 rows).
func TestSeriesFreshenerHolder_StalePath_SpawnsHandleForcedAsync(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "missing_lang"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 25551, "ru-RU")
	require.True(t, res.Refreshed)

	// HandleForcedLang is sync (=1 immediately). HandleForced is async via
	// the Story 547 goroutine — wait up to 2s. fakeWorker.HandleForced
	// returns immediately (no blocking config), so the goroutine completes
	// fast.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.handleForcedCalls.Load() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"Stage 1+2 sync path: exactly one HandleForcedLang call")
	assert.Equal(t, int64(1), w.handleForcedCalls.Load(),
		"Story 547: async HandleForced follow-up MUST fire (TTL-bypassing Stage 3-6 path)")
	assert.Equal(t, int64(0), w.handleCalls.Load(),
		"Story 547: follow-up MUST NOT route through Handle (its TTL gate would short-circuit with fresh_skip)")
}

// Story 547: when HandleForcedLang fails (timeout/error path), the
// Story 547 async HandleForced follow-up MUST NOT spawn — there's
// nothing to follow up on, Stage 1+2 was not committed. EnqueueIfStale
// stays as the fallback path.
func TestSeriesFreshenerHolder_ErrorPath_NoAsyncFollowup(t *testing.T) {
	t.Parallel()
	probe := &fakeProbe{stale: true, reason: "ttl"}
	enr := &fakeAsyncEnricher{}
	h := newFreshener(t, probe, enr, time.Second)
	w := &fakeWorker{err: errors.New("boom")}
	h.Set(w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "en-US")
	assert.True(t, res.Degraded)

	// Give any rogue goroutine room to fire.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"Sync HandleForcedLang fires (and returns err)")
	assert.Equal(t, int64(0), w.handleForcedCalls.Load(),
		"Story 547: no async HandleForced on error path")
}
