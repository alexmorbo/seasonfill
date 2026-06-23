package app_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- fakes ---

type fakeRepo struct {
	mu sync.Mutex
	// stale[kind|param|lang] → IsStale answer
	stale map[string]bool
	// last refresh timestamps
	lastAt map[string]time.Time
	// replaced[kind|param|lang] = items written (last call wins)
	replaced map[string][]disco.Item
	// replaceErr seeded → ReplaceList returns it
	replaceErr error
	// isStaleErr seeded → IsStale returns it
	isStaleErr error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		stale:    map[string]bool{},
		lastAt:   map[string]time.Time{},
		replaced: map[string][]disco.Item{},
	}
}

func keyFor(kind disco.Kind, param, lang string) string {
	return string(kind) + "|" + param + "|" + lang
}

func (r *fakeRepo) GetRanked(_ context.Context, _ disco.Kind, _, _ string, _, _ int) (disco.Page, error) {
	return disco.Page{}, errors.New("not implemented")
}

func (r *fakeRepo) IsStale(_ context.Context, kind disco.Kind, param, language string, _ time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isStaleErr != nil {
		return false, r.isStaleErr
	}
	// Default = stale (cold start).
	if v, ok := r.stale[keyFor(kind, param, language)]; ok {
		return v, nil
	}
	return true, nil
}

func (r *fakeRepo) LastRefreshedAt(_ context.Context, kind disco.Kind, param, language string) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastAt[keyFor(kind, param, language)], nil
}

func (r *fakeRepo) ReplaceList(_ context.Context, kind disco.Kind, param, language string, items []disco.Item) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replaceErr != nil {
		return r.replaceErr
	}
	r.replaced[keyFor(kind, param, language)] = items
	r.lastAt[keyFor(kind, param, language)] = time.Now()
	return nil
}

func (r *fakeRepo) replacedCount(kind disco.Kind, param, lang string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.replaced[keyFor(kind, param, lang)])
}

type fakeLangs struct {
	langs []string
	err   error
}

func (f *fakeLangs) ActiveLanguages(_ context.Context) ([]string, error) {
	return f.langs, f.err
}

type fakeStubs struct {
	mu       sync.Mutex
	calls    int
	nextID   int64
	err      error
	idForTMD map[shareddomain.TMDBID]shareddomain.SeriesID
}

func newFakeStubs() *fakeStubs {
	return &fakeStubs{idForTMD: map[shareddomain.TMDBID]shareddomain.SeriesID{}}
}

func (s *fakeStubs) EnsureStub(_ context.Context, tmdbID shareddomain.TMDBID, _ string, _, _ *string) (shareddomain.SeriesID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return 0, s.err
	}
	if id, ok := s.idForTMD[tmdbID]; ok {
		return id, nil
	}
	s.nextID++
	id := shareddomain.SeriesID(s.nextID)
	s.idForTMD[tmdbID] = id
	return id, nil
}

func (s *fakeStubs) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakeTMDB struct {
	mu       sync.Mutex
	trending int
	popular  int
	discover int
	resp     *tmdb.TVListResponse
	err      error
}

func (c *fakeTMDB) Trending(_ context.Context, _ tmdb.TrendingScope, _ string, _ int) (*tmdb.TVListResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trending++
	if c.err != nil {
		return nil, c.err
	}
	return c.resp, nil
}

func (c *fakeTMDB) Popular(_ context.Context, _ string, _ int) (*tmdb.TVListResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.popular++
	if c.err != nil {
		return nil, c.err
	}
	return c.resp, nil
}

func (c *fakeTMDB) DiscoverTV(_ context.Context, _ tmdb.DiscoverFilter, _ int) (*tmdb.TVListResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.discover++
	if c.err != nil {
		return nil, c.err
	}
	return c.resp, nil
}

func (c *fakeTMDB) trendingCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.trending
}

func (c *fakeTMDB) popularCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.popular
}

func (c *fakeTMDB) discoverCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.discover
}

type fakeTopKinds struct {
	genres   []int
	networks []int
	err      error
}

func (f *fakeTopKinds) TopGenres(_ context.Context, _ int) ([]int, error) {
	return f.genres, f.err
}

func (f *fakeTopKinds) TopNetworks(_ context.Context, _ int) ([]int, error) {
	return f.networks, f.err
}

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

// --- fixtures ---

func makeResp(n int) *tmdb.TVListResponse {
	results := make([]tmdb.TVListEntry, n)
	for i := range n {
		results[i] = tmdb.TVListEntry{
			ID:           int64(i + 1),
			Name:         "Show " + strconv.Itoa(i+1),
			FirstAirDate: "2020-01-01",
			PosterPath:   "/p.jpg",
		}
	}
	return &tmdb.TVListResponse{Page: 1, Results: results, TotalPages: 1, TotalResults: n}
}

func newTestWorker(t *testing.T, repo *fakeRepo, langs *fakeLangs, stubs *fakeStubs, client *fakeTMDB, tops *fakeTopKinds) *app.Worker {
	t.Helper()
	return app.NewWorker(app.WorkerDeps{
		Repo:     repo,
		Langs:    langs,
		Stubs:    stubs,
		TMDB:     client,
		TopKinds: tops,
		Log:      slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:    &fixedClock{now: time.Unix(1_700_000_000, 0)},
	})
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(b []byte) (int, error) {
	w.t.Log(string(b))
	return len(b), nil
}

// --- tests ---

func TestTick_EmptyLanguages_NoTMDBCalls(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: nil}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))
	require.Zero(t, client.trendingCalls())
	require.Zero(t, client.popularCalls())
	require.Zero(t, client.discoverCalls())
}

func TestTick_OneLanguage_ThreeLeaderboardCalls(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{} // empty catalog → no curated refreshes

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))

	// Trending day + Trending week = 2 Trending calls.
	require.Equal(t, 2, client.trendingCalls())
	require.Equal(t, 1, client.popularCalls())
	require.Zero(t, client.discoverCalls(), "empty top_kinds → no DiscoverTV calls")
}

func TestTick_PopulatesItemsAndReplacesOncePerKind(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(20)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))

	// 5 pages of 20 (the fake returns the same 20-entry response on every
	// page; TotalPages=1 short-circuits after page 1 — net 20 items per
	// leaderboard kind).
	require.Equal(t, 20, repo.replacedCount(disco.KindTrendingDay, "", "en-US"))
	require.Equal(t, 20, repo.replacedCount(disco.KindTrendingWeek, "", "en-US"))
	require.Equal(t, 20, repo.replacedCount(disco.KindPopular, "", "en-US"))
	// Stub-upsert: 20 unique ids × 3 leaderboards = 60 calls (each
	// stub call records via per-tmdb-id memoisation in the fake, but
	// the worker still issues 60 EnsureStub calls — that's the spec).
	require.Equal(t, 60, stubs.callCount())
}

func TestTick_NotStale_SkipsRefresh(t *testing.T) {
	repo := newFakeRepo()
	// Mark every leaderboard fresh; curated stays stale.
	for _, k := range []disco.Kind{disco.KindTrendingDay, disco.KindTrendingWeek, disco.KindPopular} {
		repo.stale[keyFor(k, "", "en-US")] = false
	}
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))
	require.Zero(t, client.trendingCalls(), "fresh trending: no calls")
	require.Zero(t, client.popularCalls(), "fresh popular: no calls")
}

func TestTick_TMDBError_OutcomeErrorAndNoReplace(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{err: errors.New("tmdb 500")}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))
	// Replace list must NOT have been called (old data preserved).
	require.Zero(t, repo.replacedCount(disco.KindTrendingDay, "", "en-US"))
}

func TestTick_TopKindsPopulated_TriggersDiscoverTV(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{
		genres:   []int{18, 35},
		networks: []int{213},
	}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))

	// 2 genres + 1 network = 3 DiscoverTV calls.
	require.Equal(t, 3, client.discoverCalls())
}

func TestTick_ExceedsMaxLanguages_TruncatesAndWarns(t *testing.T) {
	repo := newFakeRepo()
	many := make([]string, app.MaxActiveLanguages+5)
	for i := range many {
		many[i] = "lang-" + strconv.Itoa(i)
	}
	langs := &fakeLangs{langs: many}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))
	// Should cap at MaxActiveLanguages × 3 leaderboard kinds = capped Trending count.
	require.Equal(t, app.MaxActiveLanguages*2, client.trendingCalls())
	require.Equal(t, app.MaxActiveLanguages, client.popularCalls())
}

func TestTick_WarmingFlipsAfterFirstSuccess(t *testing.T) {
	// Verifies SetDiscoveryWarming(false) is invoked at least once;
	// we can't easily intercept the VictoriaMetrics gauge here so this
	// test asserts the side-effect indirectly via a second Tick not
	// crashing on the CompareAndSwap path.
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(5)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))
	require.NoError(t, w.Tick(context.Background()), "second tick must not re-flip warming")
}

func TestWorker_ThrottlesRefreshRate(t *testing.T) {
	repo := newFakeRepo()
	// 2 langs × 3 leaderboards = 6 refreshes. With burst=2 and rps=20,
	// 4 of those 6 must block on the limiter — measurable wall-clock.
	langs := &fakeLangs{langs: []string{"en-US", "ru-RU"}}
	client := &fakeTMDB{resp: makeResp(1)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	limiter := rate.NewLimiter(rate.Limit(20), 2)
	w := app.NewWorker(app.WorkerDeps{
		Repo:     repo,
		Langs:    langs,
		Stubs:    stubs,
		TMDB:     client,
		TopKinds: tops,
		Log:      slog.New(slog.NewTextHandler(testWriter{t: t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Clock:    &fixedClock{now: time.Unix(1_700_000_000, 0)},
		Limiter:  limiter,
	})

	startWall := time.Now()
	require.NoError(t, w.Tick(context.Background()))
	elapsed := time.Since(startWall)

	// 6 refreshes - 2 burst = 4 throttled at 20rps → ≈200ms minimum.
	// Generous slack for CI flake.
	require.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
		"limiter must delay tick beyond burst budget")
	require.Less(t, elapsed, 2*time.Second,
		"limiter must not stall (sanity)")

	// All 6 leaderboard refreshes still landed.
	require.Equal(t, 4, client.trendingCalls(), "2 langs × 2 trending kinds = 4")
	require.Equal(t, 2, client.popularCalls(), "2 langs × 1 popular kind = 2")
}

func TestWorker_NilLimiterDefaultsToProduction(t *testing.T) {
	// Sanity: omitting the limiter yields a working worker. Production
	// burst (20) easily absorbs a single-lang single-tick test, so no
	// measurable delay should leak through.
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(1)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops) // no Limiter
	require.NoError(t, w.Tick(context.Background()))
	require.Equal(t, 2, client.trendingCalls())
	require.Equal(t, 1, client.popularCalls())
}

func TestRunForever_FirstTickImmediate_AndHonoursContext(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(1)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		w.RunForever(ctx, 100*time.Millisecond)
		close(done)
	}()

	// Cold-start tick fires immediately — give it ≤200ms to land.
	require.Eventually(t, func() bool {
		return client.popularCalls() > 0
	}, 2*time.Second, 10*time.Millisecond, "cold-start tick must populate within 2s")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunForever did not honour ctx cancellation")
	}
}
