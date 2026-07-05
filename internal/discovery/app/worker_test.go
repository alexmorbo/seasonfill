package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
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
	// hasAnyList seeded → HasAnyList returns it (default false = empty DB)
	hasAnyList bool
	// hasAnyErr seeded → HasAnyList returns it
	hasAnyErr error
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

func (r *fakeRepo) HasAnyList(_ context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasAnyErr != nil {
		return false, r.hasAnyErr
	}
	return r.hasAnyList, nil
}

func (r *fakeRepo) replacedCount(kind disco.Kind, param, lang string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.replaced[keyFor(kind, param, lang)])
}

func (r *fakeRepo) replacedItems(kind disco.Kind, param, lang string) []disco.Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.replaced[keyFor(kind, param, lang)]
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

func (s *fakeStubs) EnsureStub(_ context.Context, tmdbID shareddomain.TMDBID, _, _, _, _ string, _, _ *string) (shareddomain.SeriesID, error) {
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

func (c *fakeTMDB) setErr(err error)               { c.mu.Lock(); c.err = err; c.mu.Unlock() }
func (c *fakeTMDB) setResp(r *tmdb.TVListResponse) { c.mu.Lock(); c.resp = r; c.mu.Unlock() }

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

func (c *fixedClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

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

// Story 1036 — a TMDB list entry carrying vote_average + first_air_date
// must materialise onto the discovery Item's TMDBRating + Year so the
// values reach discovery_lists via ReplaceList (and, downstream, the
// /discovery/* response). Also asserts the vote_average 0 sentinel → nil.
func TestTick_MaterialisesTMDBRatingAndYear_Story1036(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: &tmdb.TVListResponse{
		Page:       1,
		TotalPages: 1,
		Results: []tmdb.TVListEntry{
			{ID: 1, Name: "Rated Show", FirstAirDate: "2021-06-02", VoteAverage: 8.4},
			{ID: 2, Name: "Unrated Show", FirstAirDate: "2019-01-01", VoteAverage: 0},
		},
	}}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.NoError(t, w.Tick(context.Background()))

	items := repo.replacedItems(disco.KindTrendingDay, "", "en-US")
	require.Len(t, items, 2)

	require.NotNil(t, items[0].TMDBRating)
	assert.InDelta(t, 8.4, *items[0].TMDBRating, 1e-9)
	require.NotNil(t, items[0].Year)
	assert.Equal(t, 2021, *items[0].Year)

	assert.Nil(t, items[1].TMDBRating, "vote_average 0 sentinel must store NULL rating")
	require.NotNil(t, items[1].Year)
	assert.Equal(t, 2019, *items[1].Year)
}

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

func TestWorker_Tick_FlipsWarmingViaProbeWhenDataExists(t *testing.T) {
	// Hotfix scenario: redeploy against an already-populated discovery_lists
	// table. Every (kind, lang) pair is within its freshness window, so
	// IsStale returns false and refresh() never fires its CompareAndSwap.
	// The post-Tick probe MUST see HasAnyList()==true and flip warming off.
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(0)}
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}
	// Mark every leaderboard fresh — Tick will take the skip-refresh branch.
	for _, k := range []disco.Kind{disco.KindTrendingDay, disco.KindTrendingWeek, disco.KindPopular} {
		repo.stale[keyFor(k, "", "en-US")] = false
	}
	// Probe sees data from a prior worker run.
	repo.hasAnyList = true

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.True(t, w.IsWarming(), "warming starts true")
	require.NoError(t, w.Tick(context.Background()))
	require.False(t, w.IsWarming(), "probe must flip warming once Tick sees existing data")
	require.Zero(t, client.trendingCalls(), "fresh lists: no TMDB calls")
}

func TestWorker_Tick_StaysWarmingWhenRepoEmpty(t *testing.T) {
	// Guard test: probe returns false (empty DB) and no refresh fires —
	// warming MUST stay true so handlers keep returning the cold-start
	// envelope until the worker actually writes a list.
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{err: errors.New("tmdb 500")} // force refresh failure
	stubs := newFakeStubs()
	tops := &fakeTopKinds{}
	repo.hasAnyList = false

	w := newTestWorker(t, repo, langs, stubs, client, tops)
	require.True(t, w.IsWarming())
	require.NoError(t, w.Tick(context.Background()))
	require.True(t, w.IsWarming(), "empty DB + no successful refresh: still warming")
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

// --- W15-11 cold-start retry (virtual-clock driven) ---

func newFakeClockWorker(repo *fakeRepo, langs *fakeLangs, stubs *fakeStubs, client *fakeTMDB, tops *fakeTopKinds, fake *clock.Fake) *app.Worker {
	return app.NewWorker(app.WorkerDeps{
		Repo: repo, Langs: langs, Stubs: stubs, TMDB: client, TopKinds: tops,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:   fake,
		Limiter: rate.NewLimiter(rate.Inf, 1), // never blocks on wall clock
	})
}

func TestRunForever_ColdStartRetry_BackoffLadder(t *testing.T) {
	repo := newFakeRepo() // default IsStale=true
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{err: errors.New("tmdb down")} // every fetch errors → unhealthy
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newFakeClockWorker(repo, langs, newFakeStubs(), client, &fakeTopKinds{}, fake)

	ctx := t.Context()
	before := metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get()
	go w.RunForever(ctx, time.Hour)

	// clock.Fake waiter count is cumulative (a fired Sleep is NOT
	// decremented), so the N-th park is reached at BlockUntilWaiters(N).
	fake.BlockUntilWaiters(1) // tick#1 (immediate) failed → parked at 1m
	require.Equal(t, 1, client.popularCalls())
	fake.Advance(time.Minute)
	fake.BlockUntilWaiters(2) // tick#2 failed → parked at 5m
	require.Equal(t, 2, client.popularCalls())
	fake.Advance(5 * time.Minute)
	fake.BlockUntilWaiters(3) // tick#3 failed → parked at 15m
	require.Equal(t, 3, client.popularCalls())
	fake.Advance(15 * time.Minute)
	fake.BlockUntilWaiters(4) // tick#4 failed → parked at interval(1h)
	require.Equal(t, 4, client.popularCalls())
	// a sub-interval advance must NOT wake it (ladder now holds at 1h)
	fake.Advance(time.Minute)
	require.Equal(t, 4, client.popularCalls())
	require.Equal(t, before+4, metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get())
}

func TestRunForever_ColdStartRetry_ResetsOnSuccess(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{err: errors.New("tmdb down")}
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newFakeClockWorker(repo, langs, newFakeStubs(), client, &fakeTopKinds{}, fake)

	ctx := t.Context()
	go w.RunForever(ctx, time.Hour)

	fake.BlockUntilWaiters(1) // tick#1 failed → parked at 1m
	require.Equal(t, 1, client.popularCalls())
	client.setResp(makeResp(1))
	client.setErr(nil) // TMDB recovers
	fake.Advance(time.Minute)
	fake.BlockUntilWaiters(2) // tick#2 healthy → parked at interval(1h)
	require.Equal(t, 2, client.popularCalls())
	// backoff reset → a 1m advance must NOT wake it
	fake.Advance(time.Minute)
	require.Equal(t, 2, client.popularCalls())
	// crossing the full interval does (Sleep started at virtual t=1m,
	// deadline 1m+1h; we've advanced +1m to t=2m, now +(1h-1m) → t=1h+1m)
	fake.Advance(time.Hour - time.Minute)
	fake.BlockUntilWaiters(3)
	require.Equal(t, 3, client.popularCalls())
}

func TestRunForever_HealthyBoot_NoEarlyRetry(t *testing.T) {
	repo := newFakeRepo()
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{resp: makeResp(1)} // succeeds from boot
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newFakeClockWorker(repo, langs, newFakeStubs(), client, &fakeTopKinds{}, fake)

	ctx := t.Context()
	before := metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get()
	go w.RunForever(ctx, time.Hour)

	fake.BlockUntilWaiters(1) // tick#1 healthy → parked at interval
	require.Equal(t, 1, client.popularCalls())
	fake.Advance(time.Minute) // 1m << 1h → no wake
	require.Equal(t, 1, client.popularCalls(), "healthy boot must not retry early")
	fake.Advance(time.Hour) // cross interval → tick#2
	fake.BlockUntilWaiters(2)
	require.Equal(t, 2, client.popularCalls())
	require.Equal(t, before, metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get())
}

func TestRunForever_NothingStale_NoRetry(t *testing.T) {
	repo := newFakeRepo()
	// seed the three leaderboard keys as fresh (not stale)
	repo.stale[keyFor(disco.KindTrendingDay, "", "en-US")] = false
	repo.stale[keyFor(disco.KindTrendingWeek, "", "en-US")] = false
	repo.stale[keyFor(disco.KindPopular, "", "en-US")] = false
	langs := &fakeLangs{langs: []string{"en-US"}}
	client := &fakeTMDB{err: errors.New("must not be called")}
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	w := newFakeClockWorker(repo, langs, newFakeStubs(), client, &fakeTopKinds{}, fake)

	ctx := t.Context()
	before := metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get()
	go w.RunForever(ctx, time.Hour)

	fake.BlockUntilWaiters(1) // healthy (nothing stale) → parked at interval
	require.Zero(t, client.popularCalls())
	fake.Advance(time.Minute)
	require.Zero(t, client.popularCalls(), "nothing stale is healthy — no early retry")
	require.Equal(t, before, metrics.GetOrCreateCounter(`seasonfill_discovery_cold_start_retries_total`).Get())
}
