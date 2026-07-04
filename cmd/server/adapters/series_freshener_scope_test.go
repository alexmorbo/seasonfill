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
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
)

// scopeProbe emits per-section verdicts from an explicit map — lets
// each A5 scope test pin exactly which sections are Stale=true.
type scopeProbe struct {
	verdicts map[freshener.Section]bool // section → Stale
	reason   string
	err      error

	mu    sync.Mutex
	calls int
}

func (p *scopeProbe) IsStale(_ context.Context, _ domain.SeriesID, _ values.LanguageTag, seasonNumbers []int) ([]freshener.SectionVerdict, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	out := make([]freshener.SectionVerdict, 0, len(freshener.FixedSections)+len(seasonNumbers))
	for _, s := range freshener.FixedSections {
		stale, ok := p.verdicts[s]
		reason := "fresh"
		if !ok {
			// Default to Fresh when not set.
			stale = false
		}
		if stale {
			reason = p.reason
		}
		out = append(out, freshener.SectionVerdict{
			Section: s, Stale: stale, Reason: reason,
		})
	}
	for _, n := range seasonNumbers {
		sec := freshener.SeasonSection(n)
		stale, ok := p.verdicts[sec]
		reason := "fresh"
		if !ok {
			stale = false
		}
		if stale {
			reason = p.reason
		}
		out = append(out, freshener.SectionVerdict{
			Section: sec, Stale: stale, Reason: reason,
		})
	}
	return out, nil
}

// sectionalErrWorker satisfies SeriesWorkerHandle with per-section error
// injection. Used by partial-failure tests: RefreshCast fails while
// RefreshSeriesText + RefreshMediaAssets succeed. Falls back to nil on
// entries not set in the map.
type sectionalErrWorker struct {
	// Per-method error injection.
	handleForcedLangErr       error
	refreshSeriesTextErr      error
	refreshCastErr            error
	refreshRecommendationsErr error
	refreshMediaAssetsErr     error
	refreshSeasonSlimErr      error

	// Counters.
	handleForcedLangCalls       atomic.Int64
	refreshSeriesTextCalls      atomic.Int64
	refreshCastCalls            atomic.Int64
	refreshRecommendationsCalls atomic.Int64
	refreshMediaAssetsCalls     atomic.Int64
	refreshSeasonSlimCalls      atomic.Int64

	// Optional per-method blocking gate (channel per method); test closes
	// it to release. Nil channel means no gate.
	blockRefreshSeriesText chan struct{}
}

func (w *sectionalErrWorker) Handle(_ context.Context, _ domain.SeriesID) error { return nil }
func (w *sectionalErrWorker) HandleForced(_ context.Context, _ domain.SeriesID) error {
	return nil
}

func (w *sectionalErrWorker) HandleForcedLang(_ context.Context, _ domain.SeriesID, _ string) error {
	w.handleForcedLangCalls.Add(1)
	return w.handleForcedLangErr
}

func (w *sectionalErrWorker) RefreshSeriesText(ctx context.Context, _ domain.SeriesID, _ string, _ bool) error {
	w.refreshSeriesTextCalls.Add(1)
	if w.blockRefreshSeriesText != nil {
		select {
		case <-w.blockRefreshSeriesText:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return w.refreshSeriesTextErr
}

// RefreshSeriesAllLangs — S-B. SectionOverview dispatches here; reuse the
// refreshSeriesText counter / err / block-gate so the existing
// SectionOverview dispatch assertions (partial-failure, singleflight,
// join-error) stay valid.
func (w *sectionalErrWorker) RefreshSeriesAllLangs(ctx context.Context, _ domain.SeriesID, _ bool) error {
	w.refreshSeriesTextCalls.Add(1)
	if w.blockRefreshSeriesText != nil {
		select {
		case <-w.blockRefreshSeriesText:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return w.refreshSeriesTextErr
}

func (w *sectionalErrWorker) RefreshCast(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	w.refreshCastCalls.Add(1)
	return w.refreshCastErr
}

func (w *sectionalErrWorker) RefreshSeasonSlim(_ context.Context, _ domain.SeriesID, _ int, _ string, _ bool) error {
	w.refreshSeasonSlimCalls.Add(1)
	return w.refreshSeasonSlimErr
}

func (w *sectionalErrWorker) RefreshRecommendations(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	w.refreshRecommendationsCalls.Add(1)
	return w.refreshRecommendationsErr
}

func (w *sectionalErrWorker) RefreshMediaAssets(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	w.refreshMediaAssetsCalls.Add(1)
	return w.refreshMediaAssetsErr
}

// newScopeHolder — freshener wired with scopeProbe + fake enricher +
// worker of caller's choice.
func newScopeHolder(t *testing.T, probe freshener.Probe, enr *fakeAsyncEnricher, timeout time.Duration, w adapters.SeriesWorkerHandle) *adapters.SeriesFreshenerHolder {
	t.Helper()
	h, err := adapters.NewSeriesFreshenerHolder(adapters.SeriesFreshenerConfig{
		Probe:         probe,
		AsyncEnricher: enr,
		SyncTimeout:   timeout,
	})
	require.NoError(t, err)
	if w != nil {
		h.Set(w)
	}
	return h
}

// ── Driver behavior tests ────────────────────────────────────────────────

func TestEnsureFreshScope_SyncMode_HappyPath(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionOverview: true,
			freshener.SectionCast:     true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Refreshed)
	assert.False(t, res.Degraded)
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load())
	assert.Equal(t, int64(1), w.refreshCastCalls.Load())
	assert.Equal(t, int64(0), w.refreshMediaAssetsCalls.Load(),
		"non-requested section MUST NOT dispatch")
}

func TestEnsureFreshScope_AsyncMode_HappyPath(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionOverview: true,
			freshener.SectionCast:     true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast},
		nil, false, seriesdetail.ModeAsync,
	)
	require.NoError(t, err)
	assert.True(t, res.Refreshed)

	// Async dispatch — goroutines finish in the background. Wait for them.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.refreshSeriesTextCalls.Load() == 1 && w.refreshCastCalls.Load() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load())
	assert.Equal(t, int64(1), w.refreshCastCalls.Load())
}

func TestEnsureFreshScope_ForceBypass(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		// Everything fresh.
		verdicts: map[freshener.Section]bool{},
		reason:   "fresh",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast},
		nil, true, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Refreshed,
		"force=true MUST dispatch all requested sections even when Probe says Fresh")
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load())
	assert.Equal(t, int64(1), w.refreshCastCalls.Load())
}

func TestEnsureFreshScope_PartialFailure(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionOverview: true,
			freshener.SectionCast:     true,
			freshener.SectionMedia:    true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &sectionalErrWorker{refreshCastErr: errors.New("cast blew up")}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast, freshener.SectionMedia},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "section=cast",
		"combined error MUST annotate the failing section")
	assert.Contains(t, err.Error(), "cast blew up")
	assert.True(t, res.Refreshed)
	assert.True(t, res.Degraded, "partial success surfaces Degraded=true")
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load(),
		"succeeded section dispatched sync only (no carry-over)")
	assert.Equal(t, int64(1), w.refreshMediaAssetsCalls.Load(),
		"succeeded section dispatched sync only (no carry-over)")
	assert.Equal(t, 1, enr.Calls(),
		"partial failure enqueues belt-and-suspenders async")
	// W15-10: the FAILED cast section carries over to the async path — sync
	// invocation (1) plus the carry-over re-dispatch (2). Async, so poll.
	require.Eventually(t, func() bool {
		return w.refreshCastCalls.Load() >= 2
	}, 3*time.Second, 10*time.Millisecond,
		"failed section MUST carry over to async (sync + carry-over = 2 calls)")
}

func TestEnsureFreshScope_SyncTimeout(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	// Block ANY dispatch: worker just waits on ctx.Done().
	w := &fakeWorker{block: true}
	h := newScopeHolder(t, probe, enr, 50*time.Millisecond, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"timeout MUST surface context.DeadlineExceeded via errors.Is")
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, 1, enr.Calls(), "timeout enqueues async fallback")
}

func TestEnsureFreshScope_Singleflight_CoalescesSameSectionLang(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	release := make(chan struct{})
	w := &sectionalErrWorker{blockRefreshSeriesText: release}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			_, _ = h.EnsureFreshScope(context.Background(), 42, "ru-RU",
				[]freshener.Section{freshener.SectionOverview},
				nil, false, seriesdetail.ModeSync,
			)
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load(),
		"singleflight MUST coalesce %d concurrent same-(id, section, lang) callers onto ONE invocation", N)
}

func TestEnsureFreshScope_Singleflight_DifferentLangsDoNotCoalesce(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	release := make(chan struct{})
	w := &sectionalErrWorker{blockRefreshSeriesText: release}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = h.EnsureFreshScope(context.Background(), 42, "ru-RU",
			[]freshener.Section{freshener.SectionOverview},
			nil, false, seriesdetail.ModeSync,
		)
	}()
	go func() {
		defer wg.Done()
		_, _ = h.EnsureFreshScope(context.Background(), 42, "en-US",
			[]freshener.Section{freshener.SectionOverview},
			nil, false, seriesdetail.ModeSync,
		)
	}()
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int64(2), w.refreshSeriesTextCalls.Load(),
		"different langs MUST NOT coalesce — different singleflight keys")
}

func TestEnsureFreshScope_Singleflight_DifferentSectionsSameLang(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionOverview: true,
			freshener.SectionCast:     true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	_, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)

	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load(),
		"Overview and Cast are DIFFERENT singleflight keys — each fires once")
	assert.Equal(t, int64(1), w.refreshCastCalls.Load())
}

func TestEnsureFreshScope_SparseSeasonVerdicts(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionMedia:     true,
			freshener.SeasonSection(8): true,
			freshener.SeasonSection(9): false, // fresh
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	_, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{
			freshener.SectionMedia,
			freshener.SeasonSection(8),
			freshener.SeasonSection(9),
		},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), w.refreshMediaAssetsCalls.Load())
	assert.Equal(t, int64(1), w.refreshSeasonSlimCalls.Load(),
		"season:8 stale → RefreshSeasonSlim fires once; season:9 fresh → skipped")
}

func TestEnsureFreshScope_ProbeError_Degraded(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{err: errors.New("db down")}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(0), w.calls.Load(),
		"probe error → no dispatch")
}

func TestEnsureFreshScope_EmptyStaleVerdicts_ReturnsFreshQuickly(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{verdicts: map[freshener.Section]bool{}, reason: "fresh"}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	start := time.Now()
	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast, freshener.SectionMedia},
		nil, false, seriesdetail.ModeSync,
	)
	dur := time.Since(start)
	require.NoError(t, err)
	assert.True(t, res.Fresh)
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(0), w.calls.Load())
	assert.Less(t, dur, 100*time.Millisecond, "fresh path should be fast")
}

func TestEnsureFreshScope_InvalidLang_DegradesToZero(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	_, err := h.EnsureFreshScope(context.Background(), 42, "not-a-valid-bcp47",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	// Narrow method fired with raw string (worker's own VO shim catches it).
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load())
	probe.mu.Lock()
	defer probe.mu.Unlock()
	assert.Equal(t, 1, probe.calls, "probe consulted despite invalid lang (zero VO)")
}

func TestEnsureFreshScope_NilInner_AsyncFallback(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	// NOTE: pass nil worker → Set not called → inner stays nil.
	h := newScopeHolder(t, probe, enr, time.Second, nil)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Degraded)
	assert.Equal(t, 1, enr.Calls(),
		"nil inner (boot race) → AsyncEnricher.EnqueueIfStale fallback")
}

func TestEnsureFreshScope_ClosedHolder_ReturnsFresh(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Fresh, "closed holder MUST return Fresh=true immediately")
	assert.Equal(t, int64(0), w.calls.Load(), "closed holder MUST NOT dispatch")
}

func TestEnsureFreshScope_SkeletonRoute_CallsHandleForcedLang(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionSkeleton: true},
		reason:   "stub",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	_, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionSkeleton},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load(),
		"SectionSkeleton MUST route through HandleForcedLang (no A2-A4 narrow method covers canon)")
	assert.Equal(t, int64(0), w.refreshSeriesTextCalls.Load(),
		"SectionSkeleton does NOT route through RefreshSeriesText")
}

// TestEnsureFreshScope_ParentCtxDone_GoroutinesSurvive verifies the
// detached-ctx invariant: when the caller cancels its ctx, the sync
// dispatch goroutines continue under the detached SyncTimeout ctx.
// This mirrors the pre-A5 singleflight leader semantics.
func TestEnsureFreshScope_ParentCtxDone_GoroutinesSurvive(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	var entry atomic.Pointer[struct{ err error }]
	releaseCh := make(chan struct{})
	w := &workerCtxRecorder{releaseCh: releaseCh, entry: &entry}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	parent, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{}, 1)
	go func() {
		_, _ = h.EnsureFreshScope(parent, 42, "ru-RU",
			[]freshener.Section{freshener.SectionOverview},
			nil, false, seriesdetail.ModeSync,
		)
		doneCh <- struct{}{}
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(releaseCh)
	<-doneCh

	state := entry.Load()
	require.NotNil(t, state)
	assert.NoError(t, state.err,
		"detached ctx invariant: goroutine MUST observe Err()=nil at entry despite parent cancel")
}

func TestEnsureFresh_ShimDelegates_LegacyBehavior(t *testing.T) {
	t.Parallel()
	// All 5 fixed sections stale → shim must dispatch all 5.
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionSkeleton:        true,
			freshener.SectionOverview:        true,
			freshener.SectionCast:            true,
			freshener.SectionRecommendations: true,
			freshener.SectionMedia:           true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res := h.EnsureFresh(context.Background(), 42, "ru-RU")
	assert.True(t, res.Refreshed)
	// Shim's canned list: [Skeleton, Overview, Cast, Recs, Media].
	assert.Equal(t, int64(1), w.handleForcedLangCalls.Load())
	assert.Equal(t, int64(1), w.refreshSeriesTextCalls.Load())
	assert.Equal(t, int64(1), w.refreshCastCalls.Load())
	assert.Equal(t, int64(1), w.refreshRecommendationsCalls.Load())
	assert.Equal(t, int64(1), w.refreshMediaAssetsCalls.Load())
}

// ── Helper unit tests ────────────────────────────────────────────────────

// TestEnsureFreshScope_MergeSeasonNumbers_DispatchOrder verifies that a
// mix of season:N Sections + explicit seasonNumbers dedupes correctly
// (through the driver, since mergeSeasonNumbers itself is unexported).
func TestEnsureFreshScope_MergeSeasonNumbers_Dedup(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SeasonSection(8):  true,
			freshener.SeasonSection(9):  true,
			freshener.SeasonSection(10): true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	// Explicit seasonNumbers=[8, 9] + sections includes season:9 (dup) +
	// season:10. Expect three unique dispatches (8, 9, 10) — not four.
	_, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{
			freshener.SeasonSection(9),
			freshener.SeasonSection(10),
		},
		[]int{8, 9}, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), w.refreshSeasonSlimCalls.Load(),
		"season:9 dedupes across explicit + section; season:10 fires; season:8 was explicit-only (no matching Section requested by caller, so not dispatched)")
}

// TestEnsureFreshScope_JoinSectionErrors_Wraps verifies the combined
// error preserves errors.Is chain to underlying section errors.
func TestEnsureFreshScope_JoinSectionErrors_Wraps(t *testing.T) {
	t.Parallel()
	overviewErr := errors.New("overview fail")
	castErr := errors.New("cast fail")
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{
			freshener.SectionOverview: true,
			freshener.SectionCast:     true,
		},
		reason: "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &sectionalErrWorker{
		refreshSeriesTextErr: overviewErr,
		refreshCastErr:       castErr,
	}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	_, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview, freshener.SectionCast},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, overviewErr), "combined err MUST chain to overview err via errors.Is")
	assert.True(t, errors.Is(err, castErr), "combined err MUST chain to cast err via errors.Is")
}

// TestEnsureFreshScope_ZeroSeriesID_Skipped — negative id short-circuits
// before Probe consultation (mirrors EnsureFresh legacy semantics).
func TestEnsureFreshScope_ZeroSeriesID_Skipped(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
	}
	enr := &fakeAsyncEnricher{}
	w := &fakeWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 0, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Fresh)
	probe.mu.Lock()
	defer probe.mu.Unlock()
	assert.Equal(t, 0, probe.calls, "probe MUST NOT be called for zero seriesID")
}

// ── W15-10 sync-budget carry-over ────────────────────────────────────────

// carryoverWorker satisfies SeriesWorkerHandle with a single instrumented
// target method (RefreshSeriesAllLangs, the SectionOverview route). A shared
// atomic counter lets the carry-over tests branch behaviour on the 1st vs the
// Nth call:
//   - blockFirst: the FIRST call blocks on ctx.Done() and returns ctx.Err()
//     (proves a sync-budget timeout is re-dispatched to async, where the 2nd
//     call returns nil); subsequent calls return nil.
//   - err: every call returns this error (proves a plain section error also
//     carries over).
//
// All other narrow methods are unused no-ops.
type carryoverWorker struct {
	overviewCalls atomic.Int64
	blockFirst    bool
	err           error
}

func (w *carryoverWorker) Handle(_ context.Context, _ domain.SeriesID) error { return nil }
func (w *carryoverWorker) HandleForced(_ context.Context, _ domain.SeriesID) error {
	return nil
}

func (w *carryoverWorker) HandleForcedLang(_ context.Context, _ domain.SeriesID, _ string) error {
	return nil
}

func (w *carryoverWorker) RefreshSeriesAllLangs(ctx context.Context, _ domain.SeriesID, _ bool) error {
	n := w.overviewCalls.Add(1)
	if w.blockFirst && n == 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	return w.err
}

func (w *carryoverWorker) RefreshSeriesText(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return nil
}

func (w *carryoverWorker) RefreshCast(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return nil
}

func (w *carryoverWorker) RefreshSeasonSlim(_ context.Context, _ domain.SeriesID, _ int, _ string, _ bool) error {
	return nil
}

func (w *carryoverWorker) RefreshRecommendations(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return nil
}

func (w *carryoverWorker) RefreshMediaAssets(_ context.Context, _ domain.SeriesID, _ string, _ bool) error {
	return nil
}

// TestEnsureFreshScope_SyncTimeout_CarriesOverToAsync — the FIRST (sync)
// dispatch blocks past the SyncTimeout budget and returns ctx.Err(); W15-10
// re-dispatches that exact section onto the async path, where the 2nd call
// returns nil. Proves timed-out sections are NOT dropped.
func TestEnsureFreshScope_SyncTimeout_CarriesOverToAsync(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &carryoverWorker{blockFirst: true}
	h := newScopeHolder(t, probe, enr, 50*time.Millisecond, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"sync-budget timeout surfaces DeadlineExceeded")
	assert.True(t, res.Degraded)
	assert.False(t, res.Refreshed)
	assert.Equal(t, 1, enr.Calls(), "belt-and-suspenders EnqueueIfStale kept")

	require.Eventually(t, func() bool {
		return w.overviewCalls.Load() >= 2
	}, 3*time.Second, 10*time.Millisecond,
		"timed-out section MUST be re-dispatched to async (carry-over)")
}

// TestEnsureFreshScope_SectionError_CarriesOverToAsync — a plain section error
// (no blocking) also carries over: once synchronously, once on the async path.
func TestEnsureFreshScope_SectionError_CarriesOverToAsync(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &carryoverWorker{err: errors.New("boom")}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.True(t, res.Degraded)
	assert.Equal(t, 1, enr.Calls(),
		"belt-and-suspenders EnqueueIfStale kept alongside carry-over")

	require.Eventually(t, func() bool {
		return w.overviewCalls.Load() >= 2
	}, 3*time.Second, 10*time.Millisecond,
		"errored section MUST be re-dispatched to async (once sync, once carry-over)")
}

// TestEnsureFreshScope_HappyPath_NoCarryOver — a fully-successful sync dispatch
// must NOT carry over and must NOT enqueue. Key regression guard against a
// double-dispatch.
func TestEnsureFreshScope_HappyPath_NoCarryOver(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &carryoverWorker{}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.NoError(t, err)
	assert.True(t, res.Refreshed)
	assert.False(t, res.Degraded)
	assert.Equal(t, int64(1), w.overviewCalls.Load(), "happy path dispatches sync only")
	assert.Equal(t, 0, enr.Calls(), "happy path MUST NOT enqueue async")

	require.Never(t, func() bool {
		return w.overviewCalls.Load() > 1
	}, 200*time.Millisecond, 20*time.Millisecond,
		"happy path MUST NOT double-dispatch via carry-over")
}

// TestEnsureFreshScope_SectionError_DegradesNotPanic — all requested sections
// fail; the caller receives a degraded result + surfaced error (no panic), and
// the composer can proceed degraded.
func TestEnsureFreshScope_SectionError_DegradesNotPanic(t *testing.T) {
	t.Parallel()
	probe := &scopeProbe{
		verdicts: map[freshener.Section]bool{freshener.SectionOverview: true},
		reason:   "missing_lang",
	}
	enr := &fakeAsyncEnricher{}
	w := &carryoverWorker{err: errors.New("boom")}
	h := newScopeHolder(t, probe, enr, time.Second, w)
	defer h.Close()

	res, err := h.EnsureFreshScope(context.Background(), 42, "ru-RU",
		[]freshener.Section{freshener.SectionOverview},
		nil, false, seriesdetail.ModeSync,
	)
	require.Error(t, err)
	assert.True(t, res.Degraded, "all-failed → Degraded=true")
	assert.False(t, res.Refreshed, "all-failed → Refreshed=false")
	assert.Contains(t, err.Error(), "section=overview",
		"combined error annotates the failing section, no panic")
}
