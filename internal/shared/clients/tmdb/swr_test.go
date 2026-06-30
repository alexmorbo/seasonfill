package tmdb

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clock"
)

// uniqueSWRCacheName mints a UUID-suffixed name so each test gets its own
// cachewatch registry entry — cachewatch.New panics on duplicate registration
// and does not unregister on Close. Mirrors the pattern in cachewatch_test.go.
func uniqueSWRCacheName(t *testing.T) string {
	t.Helper()
	return "tmdb_swr_test_" + uuid.NewString()
}

// fakeFetcher is the injectable transport for swr_test.go. It counts calls
// and returns a canned body/error pair per invocation. A optional barrier
// channel blocks the call until released — used by the singleflight tests
// to deterministically observe stale-window dedupe before the leader's
// goroutine returns.
type fakeFetcher struct {
	mu         sync.Mutex
	calls      atomic.Int64
	body       []byte
	err        error
	barrier    chan struct{} // nil → no barrier
	lastPath   string
	lastQuery  url.Values
	perCall    map[string][]byte // path → body override
	perCallErr map[string]error
}

func (f *fakeFetcher) fetch(ctx context.Context, path string, query url.Values) ([]byte, error) {
	if f.barrier != nil {
		select {
		case <-f.barrier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.calls.Add(1)
	f.mu.Lock()
	f.lastPath = path
	f.lastQuery = query
	f.mu.Unlock()
	if f.perCallErr != nil {
		if e, ok := f.perCallErr[path]; ok && e != nil {
			return nil, e
		}
	}
	if f.perCall != nil {
		if b, ok := f.perCall[path]; ok {
			return b, nil
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.body, nil
}

func newTestSWR(t *testing.T, clk clock.Clock, f *fakeFetcher) *swrCache {
	t.Helper()
	s := newSWRCache(uniqueSWRCacheName(t), clk, f.fetch)
	t.Cleanup(func() { s.Close() })
	return s
}

// TestResolveTier_AllRows verifies every PLAN-2026-07-01 §5.4 row resolves
// to the expected (TTL, label). Includes the /tv/<id>/season/<n> shadow
// case and the default fallback. Story 553 (E-1 Z4).
func TestResolveTier_AllRows(t *testing.T) {
	cases := []struct {
		name string
		path string
		ttl  time.Duration
		tier string
	}{
		{"tv_popular", "/tv/popular", 30 * time.Minute, "tv_popular"},
		{"trending_day", "/trending/tv/day", 15 * time.Minute, "trending_tv"},
		{"trending_week", "/trending/tv/week", 15 * time.Minute, "trending_tv"},
		{"discover_tv", "/discover/tv", 30 * time.Minute, "discover_tv"},
		{"search_tv", "/search/tv", 30 * time.Minute, "search_tv"},
		{"tv_canon", "/tv/12345", 12 * time.Hour, "tv_canon"},
		{"tv_season_shadow", "/tv/12345/season/2", 6 * time.Hour, "tv_season"},
		{"genre_tv_list", "/genre/tv/list", 24 * time.Hour, "genre_tv_list"},
		{"find_external", "/find/tt1234567", 24 * time.Hour, "find_external"},
		{"person_canon", "/person/42", 12 * time.Hour, "person_canon"},
		{"unknown_default", "/some/unknown/path", swrDefaultTTL, "default"},
		{"empty_default", "", swrDefaultTTL, "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ttl, tier := resolveTier(tc.path)
			assert.Equal(t, tc.ttl, ttl)
			assert.Equal(t, tc.tier, tier)
		})
	}
}

// TestCacheKey_Stability verifies the canonical (path, query) → string
// invariant. Path-only entries omit the "?" sentinel; same query keys in
// any insertion order produce identical encoded forms (url.Values is
// alphabetical via Encode). Story 553 (E-1 Z4).
func TestCacheKey_Stability(t *testing.T) {
	pathOnly := cacheKey("/tv/1", nil)
	assert.Equal(t, "/tv/1", pathOnly)

	pathEmpty := cacheKey("/tv/1", url.Values{})
	assert.Equal(t, "/tv/1", pathEmpty)

	q1 := url.Values{}
	q1.Set("language", "en-US")
	q1.Set("append_to_response", "credits")
	k1 := cacheKey("/tv/1", q1)

	q2 := url.Values{}
	q2.Set("append_to_response", "credits")
	q2.Set("language", "en-US")
	k2 := cacheKey("/tv/1", q2)
	assert.Equal(t, k1, k2, "cache key must be insertion-order-independent")

	// Different paths or different queries → different keys.
	q3 := url.Values{}
	q3.Set("language", "ru-RU")
	k3 := cacheKey("/tv/1", q3)
	assert.NotEqual(t, k1, k3)
}

// TestSWR_Miss_FetchOK exercises the cache-miss branch: synchronous fetch,
// cache populate, second call returns cached. Story 553 (E-1 Z4).
func TestSWR_Miss_FetchOK(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	f := &fakeFetcher{body: []byte(`{"id":1}`)}
	s := newTestSWR(t, clk, f)

	body, err := s.getRolling(context.Background(), "/tv/1", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"id":1}`), body)
	assert.EqualValues(t, 1, f.calls.Load())

	// Second call — fresh hit, no new fetch.
	body, err = s.getRolling(context.Background(), "/tv/1", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"id":1}`), body)
	assert.EqualValues(t, 1, f.calls.Load(), "second call should be a cache hit")
}

// TestSWR_Miss_FetchError verifies that a sync-fetch error on cold miss is
// surfaced verbatim and the cache stays empty. Story 553 (E-1 Z4).
func TestSWR_Miss_FetchError(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	f := &fakeFetcher{err: errors.New("upstream boom")}
	s := newTestSWR(t, clk, f)

	body, err := s.getRolling(context.Background(), "/tv/1", nil)
	require.Error(t, err)
	assert.Nil(t, body)
	assert.EqualValues(t, 1, f.calls.Load())

	// Retry — still a miss because the cache was never populated.
	_, err = s.getRolling(context.Background(), "/tv/1", nil)
	require.Error(t, err)
	assert.EqualValues(t, 2, f.calls.Load())
}

// installFakeInsertedAt wires a test-only insertedAt source onto the SWR
// cache so the *clock.Fake age math is decoupled from cachewatch's internal
// time.Now() at Add. Returns a setter the test calls to register insertedAt
// per key. Calls before the first getRolling are safe (no lock needed: the
// test is single-writer, multi-reader after handoff).
func installFakeInsertedAt(s *swrCache) func(key string, t time.Time) {
	var mu sync.Mutex
	stamps := map[string]time.Time{}
	s.insertedAtOverride = func(key string) (time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		t, ok := stamps[key]
		return t, ok
	}
	return func(key string, t time.Time) {
		mu.Lock()
		stamps[key] = t
		mu.Unlock()
	}
}

// TestSWR_FreshHit_NoRefresh verifies that within the fresh window (age <
// TTL - staleGrace) the wrapper returns the cached body without spawning
// a refresh. Story 553 (E-1 Z4).
func TestSWR_FreshHit_NoRefresh(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	f := &fakeFetcher{body: []byte(`{"id":1}`)}
	s := newTestSWR(t, clk, f)
	setStamp := installFakeInsertedAt(s)

	// Seed.
	_, err := s.getRolling(context.Background(), "/tv/1", nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, f.calls.Load())
	// Anchor insertedAt to the clock's NOW so age math is deterministic.
	setStamp(cacheKey("/tv/1", nil), clk.Now())

	// /tv/1 → 12h TTL, stale grace 90s. Advance 10 minutes — far inside fresh.
	clk.Advance(10 * time.Minute)

	body, err := s.getRolling(context.Background(), "/tv/1", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"id":1}`), body)
	assert.EqualValues(t, 1, f.calls.Load(), "fresh hit MUST NOT spawn a refresh")
}

// TestSWR_StaleHit_KicksRefresh verifies that within the stale-grace
// window the wrapper returns the cached body INSTANTLY and a background
// refresh lands. Story 553 (E-1 Z4).
func TestSWR_StaleHit_KicksRefresh(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	// /discover/tv → 30m TTL, stale grace 90s. Two distinct bodies to
	// confirm the refresh actually overwrites the cache entry.
	bodyA := []byte(`{"page":1,"v":"A"}`)
	bodyB := []byte(`{"page":1,"v":"B"}`)
	var doneRefresh sync.WaitGroup
	doneRefresh.Add(1)
	f := &fakeFetcher{body: bodyA}
	f.perCall = map[string][]byte{"/discover/tv": bodyA}

	s := newTestSWR(t, clk, f)
	setStamp := installFakeInsertedAt(s)

	// Cold fetch — populates with bodyA.
	body, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body)
	require.EqualValues(t, 1, f.calls.Load())

	// Anchor insertedAt at the current clock and advance into stale-grace.
	setStamp(cacheKey("/discover/tv", nil), clk.Now())

	// Switch the fake to return bodyB on the next call.
	f.perCall["/discover/tv"] = bodyB

	// Wrap fetch to release the WG once the refresh lands (calls.Load() > 1).
	origFetch := f.fetch
	s.fetch = func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		b, e := origFetch(ctx, path, q)
		if f.calls.Load() > 1 {
			doneRefresh.Done()
		}
		return b, e
	}

	// Advance into stale-grace window: TTL=30m, grace=90s → age must be in
	// [28m30s, 30m). Pick 29m30s.
	clk.Advance(29*time.Minute + 30*time.Second)

	// Stale hit — returns bodyA immediately, kicks background.
	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body, "stale hit serves the cached body INSTANTLY")

	// Wait for background refresh to land (deterministic via WG).
	doneRefresh.Wait()
	assert.EqualValues(t, 2, f.calls.Load(), "stale grace must spawn exactly one refresh")

	// Next call should pick up the freshened body. Re-stamp insertedAt at
	// the current clock so the freshened body looks "just added".
	setStamp(cacheKey("/discover/tv", nil), clk.Now())
	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyB, body, "next call after refresh sees freshened body")
}

// TestSWR_ExpiredHit_SyncFetchOK verifies that age >= TTL forces a sync
// fetch which overwrites the entry. Story 553 (E-1 Z4).
func TestSWR_ExpiredHit_SyncFetchOK(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	bodyA := []byte(`{"v":"A"}`)
	bodyB := []byte(`{"v":"B"}`)
	f := &fakeFetcher{body: bodyA}
	s := newTestSWR(t, clk, f)
	setStamp := installFakeInsertedAt(s)

	// /discover/tv → 30m TTL. Seed and anchor insertedAt.
	body, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body)
	setStamp(cacheKey("/discover/tv", nil), clk.Now())

	// Flip canned body.
	f.body = bodyB

	// Advance past hard TTL.
	clk.Advance(45 * time.Minute)

	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyB, body, "hard expiry must fetch fresh body synchronously")
	assert.EqualValues(t, 2, f.calls.Load())
}

// TestSWR_ExpiredHit_SyncFetchError verifies that on hard expiry the
// EXPIRED body is NOT served when the sync fetch fails. Story 553 (E-1 Z4).
func TestSWR_ExpiredHit_SyncFetchError(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	bodyA := []byte(`{"v":"A"}`)
	f := &fakeFetcher{body: bodyA}
	s := newTestSWR(t, clk, f)
	setStamp := installFakeInsertedAt(s)

	// /discover/tv → 30m TTL.
	_, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	setStamp(cacheKey("/discover/tv", nil), clk.Now())

	// Flip the fake to error.
	f.body = nil
	f.err = errors.New("upstream 503")

	// Past hard TTL.
	clk.Advance(45 * time.Minute)

	body, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.Error(t, err)
	assert.Nil(t, body, "hard expiry + fetch error must NOT serve stale body")
}

// TestSWR_StaleRefreshError_KeepsCachedBody verifies that a failed
// background refresh leaves the existing cache entry untouched — the next
// stale-grace hit still serves the original body and retries the refresh.
// Story 553 (E-1 Z4).
func TestSWR_StaleRefreshError_KeepsCachedBody(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	bodyA := []byte(`{"v":"A"}`)
	f := &fakeFetcher{body: bodyA}

	// Use a channel-based barrier-counting wrapper installed ONCE so we
	// don't mutate s.fetch concurrently with goroutine reads.
	refreshLanded := make(chan int, 8)
	wrappedFetch := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		b, e := f.fetch(ctx, path, q)
		// Best-effort non-blocking signal: each refresh lands a token.
		select {
		case refreshLanded <- int(f.calls.Load()):
		default:
		}
		return b, e
	}
	s := newSWRCache(uniqueSWRCacheName(t), clk, wrappedFetch)
	t.Cleanup(func() { s.Close() })
	setStamp := installFakeInsertedAt(s)

	// Cold seed under /discover/tv (30m TTL).
	body, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body)
	require.EqualValues(t, 1, f.calls.Load())
	setStamp(cacheKey("/discover/tv", nil), clk.Now())
	// Drain the cold-seed signal.
	<-refreshLanded

	// Configure refresh to error.
	f.body = nil
	f.err = errors.New("upstream blip")

	// Advance into stale-grace window.
	clk.Advance(29*time.Minute + 30*time.Second)

	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body, "stale hit serves the cached body even when the refresh is about to fail")

	// Wait for the first refresh to complete.
	select {
	case <-refreshLanded:
	case <-time.After(5 * time.Second):
		t.Fatal("first background refresh did not land within 5s")
	}
	assert.EqualValues(t, 2, f.calls.Load(), "background refresh fired once")

	// Subsequent stale hit — body MUST still be bodyA (failed refresh
	// preserves the entry). A new background refresh fires.
	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body, "failed refresh must NOT evict the cache entry")

	select {
	case <-refreshLanded:
	case <-time.After(5 * time.Second):
		t.Fatal("second background refresh did not land within 5s")
	}
	assert.EqualValues(t, 3, f.calls.Load(), "second refresh fires after first failed")
}

// TestSWR_KickRefresh_DecoupledFromCallerCtx verifies that cancelling the
// caller's ctx immediately after the stale-hit return does NOT abort the
// background refresh — the refresh uses context.Background() with a 30s
// timeout. Story 553 (E-1 Z4).
func TestSWR_KickRefresh_DecoupledFromCallerCtx(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	bodyA := []byte(`{"v":"A"}`)
	bodyB := []byte(`{"v":"B"}`)
	f := &fakeFetcher{body: bodyA}
	s := newTestSWR(t, clk, f)
	setStamp := installFakeInsertedAt(s)

	_, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, f.calls.Load())
	setStamp(cacheKey("/discover/tv", nil), clk.Now())

	// Flip body for the upcoming refresh.
	f.body = bodyB

	// Wait group released by background refresh.
	var wg sync.WaitGroup
	wg.Add(1)
	origFetch := f.fetch
	s.fetch = func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		b, e := origFetch(ctx, path, q)
		if f.calls.Load() > 1 {
			wg.Done()
		}
		return b, e
	}

	// Advance into stale-grace window.
	clk.Advance(29*time.Minute + 30*time.Second)

	// Caller passes a ctx that we cancel immediately after the call returns.
	callerCtx, cancel := context.WithCancel(context.Background())
	body, err := s.getRolling(callerCtx, "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body)
	cancel() // cancel caller's ctx — background refresh MUST survive.

	wg.Wait()
	assert.EqualValues(t, 2, f.calls.Load(), "background refresh must complete despite caller ctx cancel")
}
