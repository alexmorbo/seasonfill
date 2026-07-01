package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakePreWarmer records every PreWarm call and returns per-(seriesID, lang)
// canned errors. Thread-safe — the worker fans out sequentially but the
// tests use assertions from the test goroutine.
type fakePreWarmer struct {
	mu    sync.Mutex
	calls []preWarmCall
	// errFor maps (seriesID, lang) → error. Nil = success (warmed).
	// Missing key = success. context.Canceled surfaces as cancelled.
	errFor map[string]error
	// blockOnFirst — if set, the first call blocks until the channel
	// is closed. Used to synthesize ctx-cancel-mid-flight.
	blockOnFirst chan struct{}
	fired        atomic.Int32
}

type preWarmCall struct {
	SeriesID shareddomain.SeriesID
	Lang     string
}

func newFakePreWarmer() *fakePreWarmer {
	return &fakePreWarmer{errFor: map[string]error{}}
}

func (f *fakePreWarmer) PreWarm(ctx context.Context, seriesID shareddomain.SeriesID, lang string) error {
	if f.blockOnFirst != nil && f.fired.Add(1) == 1 {
		select {
		case <-f.blockOnFirst:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	f.calls = append(f.calls, preWarmCall{SeriesID: seriesID, Lang: lang})
	err := f.errFor[keyForCall(seriesID, lang)]
	f.mu.Unlock()
	return err
}

func (f *fakePreWarmer) callsSnapshot() []preWarmCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]preWarmCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakePreWarmer) setErr(seriesID shareddomain.SeriesID, lang string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errFor[keyForCall(seriesID, lang)] = err
}

func keyForCall(seriesID shareddomain.SeriesID, lang string) string {
	return lang + "|" + intToStr(int64(seriesID))
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// newPreWarmTestWorker builds a *app.Worker configured for pre-warm unit
// tests. TMDB fake returns a canned N-entry response so exactly one
// (kind, param, lang) refresh fires per Tick. The pre-warmer records
// every PreWarm invocation.
func newPreWarmTestWorker(t *testing.T, langs []string, itemCount int, pw *fakePreWarmer, limiter *rate.Limiter) (*app.Worker, *fakeTMDB, *fakeRepo, *fakeStubs) {
	t.Helper()
	repo := newFakeRepo()
	tmdbClient := &fakeTMDB{resp: makeResp(itemCount)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Inf, 1)
	}
	w := app.NewWorker(app.WorkerDeps{
		Repo:      repo,
		Langs:     &fakeLangs{langs: langs},
		Stubs:     stubs,
		TMDB:      tmdbClient,
		TopKinds:  tops,
		Log:       slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:     &fixedClock{now: time.Unix(1_700_000_000, 0)},
		Limiter:   limiter,
		PreWarmer: pw,
	})
	return w, tmdbClient, repo, stubs
}

// --- tests ---

// TestPreWarm_HappyPath_MultipleLangs — 3 leaderboards × 5 items × 2
// langs, all PreWarmer calls succeed → 30 total PreWarm calls
// (3 lists × 5 items × 2 langs). Per-lang loop hits every langs entry.
func TestPreWarm_HappyPath_MultipleLangs(t *testing.T) {
	pw := newFakePreWarmer()
	w, tmdbClient, _, _ := newPreWarmTestWorker(t, []string{"en-US", "ru-RU"}, 5, pw, nil)

	require.NoError(t, w.Tick(context.Background()))

	// Tick fans out: 2 langs × 3 leaderboards = 6 successful refresh() calls,
	// each producing 5 items × 2 activeLangs pre-warm pairs = 60 total.
	// TMDB call count guards vs stub upsert accidentally routing there.
	require.Equal(t, 4, tmdbClient.trendingCalls(), "2 langs × 2 trending kinds")
	require.Equal(t, 2, tmdbClient.popularCalls(), "2 langs × 1 popular kind")

	calls := pw.callsSnapshot()
	require.Len(t, calls, 60, "6 refreshes × 5 items × 2 activeLangs")

	// Every (lang, seriesID) pair must be represented at least once. The
	// dedup guarantees per-refresh unique seriesIDs; 6 refreshes × 5 =
	// 30 unique (per lang) but each lang gets refreshed 3 times so we
	// see each (seriesID, lang) 3 times across the two active langs.
	counts := make(map[string]int)
	for _, c := range calls {
		counts[keyForCall(c.SeriesID, c.Lang)]++
	}
	// We expect (seriesID=1..5, lang=en-US) each seen 6 times (3 leaderboards
	// × 2 language-iterations of the outer refresh loop).
	for sid := int64(1); sid <= 5; sid++ {
		for _, lang := range []string{"en-US", "ru-RU"} {
			require.Equal(t, 6, counts[keyForCall(shareddomain.SeriesID(sid), lang)],
				"sid=%d lang=%s must be pre-warmed 6 times (3 leaderboards × 2 outer langs)", sid, lang)
		}
	}
}

// TestPreWarm_NilPreWarmer_NoOp — no PreWarmer wired → refresh() success
// branch skips the fan-out. Verifies the nil-safe guard is present.
func TestPreWarm_NilPreWarmer_NoOp(t *testing.T) {
	// nil PreWarmer via newTestWorker (no Limiter, no PreWarmer).
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US", "ru-RU"}}
	client := &fakeTMDB{resp: makeResp(5)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := app.NewWorker(app.WorkerDeps{
		Repo:     repo,
		Langs:    langs,
		Stubs:    stubs,
		TMDB:     client,
		TopKinds: tops,
		Log:      slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:    &fixedClock{now: time.Unix(1_700_000_000, 0)},
		// no PreWarmer
	})
	require.NoError(t, w.Tick(context.Background()))
	// Refresh side of Tick still fires — 4 trending, 2 popular.
	require.Equal(t, 4, client.trendingCalls())
	require.Equal(t, 2, client.popularCalls())
	// nothing else to assert — pre-warm is a no-op with no observable
	// side-effect. Absence of panic + successful Tick suffices.
}

// TestPreWarm_EmptyActiveLangs_NoOp — RefreshNow path passes
// activeLangs=nil so preWarmSeriesTexts must short-circuit before any
// pre-warm call.
func TestPreWarm_EmptyActiveLangs_NoOp(t *testing.T) {
	pw := newFakePreWarmer()
	w, tmdbClient, _, _ := newPreWarmTestWorker(t, []string{"en-US"}, 5, pw, nil)

	require.NoError(t, w.RefreshNow(context.Background(), disco.KindTrendingDay, "", "en-US"))
	require.Equal(t, 1, tmdbClient.trendingCalls())
	require.Empty(t, pw.callsSnapshot(), "RefreshNow (activeLangs=nil) must skip pre-warm fan-out")
}

// TestPreWarm_CtxCancelMidFlight_BreaksLoop — cancel ctx after the first
// PreWarm and confirm the outer loop stops without panic. Uses a
// blocking fake to synchronise.
func TestPreWarm_CtxCancelMidFlight_BreaksLoop(t *testing.T) {
	pw := newFakePreWarmer()
	pw.blockOnFirst = make(chan struct{})

	w, _, _, _ := newPreWarmTestWorker(t, []string{"en-US", "ru-RU"}, 5, pw, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Tick(ctx)
	}()

	// Wait for the first pre-warm call to land.
	require.Eventually(t, func() bool {
		return pw.fired.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)

	// Cancel and unblock — the worker should propagate cancellation
	// through the pre-warm loop.
	cancel()
	close(pw.blockOnFirst)

	select {
	case err := <-done:
		// Tick returns ctx.Err() when cancelled between refreshes.
		// The specific error is fine so long as no panic.
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("Tick did not return after ctx cancel")
	}

	// PreWarm calls should be far fewer than the full 60 that a
	// happy-path Tick would produce.
	calls := pw.callsSnapshot()
	require.Less(t, len(calls), 60, "ctx cancel must short-circuit pre-warm fan-out")
}

// TestPreWarm_ErrorAbsorbed_ContinuesToNext — one PreWarmer error must
// not stop the rest of the fan-out.
func TestPreWarm_ErrorAbsorbed_ContinuesToNext(t *testing.T) {
	pw := newFakePreWarmer()
	// Seed error for series_id=3 en-US. Other 4 series + ru-RU succeed.
	pw.setErr(shareddomain.SeriesID(3), "en-US", errors.New("tmdb 429"))

	w, _, _, _ := newPreWarmTestWorker(t, []string{"en-US", "ru-RU"}, 5, pw, nil)
	require.NoError(t, w.Tick(context.Background()))

	// Full 60 calls should still have landed — errors don't short-circuit.
	require.Len(t, pw.callsSnapshot(), 60)
}

// TestPreWarm_LimiterShared_RespectsWait — with a slow limiter, the
// combined refresh() + prewarm rate is bounded. Measures wall-clock to
// verify preWarmSeriesTexts calls Wait() before each PreWarm.
func TestPreWarm_LimiterShared_RespectsWait(t *testing.T) {
	// burst=1 rps=Inf → wait on first token; subsequent instantly.
	// Actually to prove the *shared* limiter is consulted, we need a
	// low rps + burst=2 configuration so we can measure a floor on
	// elapsed. 1 lang × 1 leaderboard × 5 items = 5 pre-warm calls.
	// With limiter=rate.Limit(50) burst=2, the first 2 pass instantly,
	// then 4 more (3 pre-warms + 1 more refresh() at least) throttle
	// at 50rps. Loose test: we assert elapsed > 20ms with slack.
	pw := newFakePreWarmer()
	limiter := rate.NewLimiter(rate.Limit(50), 2)
	w, _, _, _ := newPreWarmTestWorker(t, []string{"en-US"}, 5, pw, limiter)

	start := time.Now()
	require.NoError(t, w.Tick(context.Background()))
	elapsed := time.Since(start)

	// 3 refreshes + 15 pre-warm calls = 18 limiter waits. Burst 2 →
	// 16 throttled at 50rps = 320ms floor. Loosen to 100ms for CI.
	require.GreaterOrEqual(t, elapsed, 100*time.Millisecond,
		"shared limiter must gate pre-warm calls")
	require.Less(t, elapsed, 5*time.Second, "sanity — limiter not stalling")

	// Should still have fired every pre-warm call.
	require.Len(t, pw.callsSnapshot(), 15, "3 leaderboards × 5 items × 1 activeLang")
}

// TestPreWarm_EmptyItems_NoOp — a refresh() that yields zero items
// (TMDB returned empty list) must not attempt any pre-warm calls.
func TestPreWarm_EmptyItems_NoOp(t *testing.T) {
	pw := newFakePreWarmer()
	w, _, _, _ := newPreWarmTestWorker(t, []string{"en-US"}, 0, pw, nil)
	require.NoError(t, w.Tick(context.Background()))
	require.Empty(t, pw.callsSnapshot(), "empty item list must skip pre-warm")
}

// TestPreWarm_PreWarmerReceivesForceFalseSemantics — ensures the
// discoveryPreWarmerHolder adapter is the pathway, and it passes
// force=false in production. We assert here through the fake's records
// that every call carried a valid seriesID and lang — the port's
// force=false semantics are validated at the adapter level; this test
// is a smoke check that the port contract is honoured (no zero
// SeriesID surfaced).
func TestPreWarm_PreWarmerReceivesForceFalseSemantics(t *testing.T) {
	pw := newFakePreWarmer()
	w, _, _, _ := newPreWarmTestWorker(t, []string{"en-US"}, 3, pw, nil)
	require.NoError(t, w.Tick(context.Background()))
	calls := pw.callsSnapshot()
	require.NotEmpty(t, calls)
	for _, c := range calls {
		require.Greater(t, int64(c.SeriesID), int64(0), "must never send zero SeriesID")
		require.NotEmpty(t, c.Lang, "must never send empty lang")
	}
}

// Verify makeResp returns a stable count so happy-path arithmetic
// stays predictable.
func TestPreWarm_makeRespGuard(t *testing.T) {
	resp := makeResp(3)
	require.Equal(t, 3, len(resp.Results))
	require.Equal(t, "Show 1", resp.Results[0].Name)
	// Sanity to avoid unused import removal by future refactors.
	_ = tmdb.TrendingDay
}
