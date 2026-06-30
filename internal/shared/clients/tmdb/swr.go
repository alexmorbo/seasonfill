// swr.go implements the stale-while-revalidate wrapper that fronts Client.do.
// Per PLAN-2026-07-01 §5.4 (porting Overseerr's getRolling, externalapi.ts:77-104).
//
// Flow:
//  1. Cache miss → SYNCHRONOUS fetch (existing do path), populate cache, return.
//  2. Cache hit, age < (TTL - swrStaleGrace) → return cached body INSTANTLY.
//  3. Cache hit, age within stale-grace window (TTL - swrStaleGrace ≤ age < TTL)
//     → return cached body INSTANTLY + spawn background refresh under a
//     first-claimer-wins sync.Map gate.
//  4. Cache hit, age >= TTL → treat as miss (synchronous fetch). On fetch error
//     the EXPIRED value is NOT returned — caller gets the error so they can
//     decide to degrade. This matches Overseerr's behaviour and keeps the
//     "stale data" exposure window bounded by TTL.
//
// Concurrency:
//   - Background refresh dedupe uses a per-key sync.Map flag (pending). The
//     FIRST stale-grace caller for a key wins LoadOrStore and spawns ONE
//     goroutine; subsequent callers see the flag set and tick
//     swr_inflight_dedup_total without spawning. The goroutine clears the
//     flag in its defer. This replaces the earlier singleflight.Group:
//     singleflight only dedupes IN-FLIGHT calls, and under a slow scheduler
//     the leader can finish before late callers reach DoChan, opening a
//     window for duplicate upstream calls (CI flake fixed in Story 553).
//   - Goroutine uses context.Background() with a 30s timeout — caller's ctx
//     would cancel the moment the user's HTTP request completes (we already
//     served them stale). Panic-recover wraps the fetch so a malformed TMDB
//     response doesn't crash a worker.
//   - Cache write is atomic only on success — a failed background refresh
//     leaves the existing entry in place. The next stale-window hit will try
//     again. (Once the entry hits hard TTL it lazy-evicts via the reaper or
//     the next GetWithAge will return age >= ttl and force a sync fetch.)
package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
)

// swrCacheSeq is a package-level monotonic counter used to mint unique cache
// names across multiple *Client constructions. cachewatch.New panics on
// duplicate name and does NOT unregister on Close; in production the reload
// subscriber Close()s the old client BEFORE building a new one, but the
// underlying cachewatch registry retains the name. Tests construct many
// clients per process, and the production reload path constructs N+1 before
// closing N. The counter sidesteps both by appending a suffix: the FIRST
// client gets "tmdb_swr", subsequent clients get "tmdb_swr_<n>".
// Story 553 (E-1 Z4) adaptation — story §12 assumes Close unregisters but
// the existing cachewatch implementation does not.
var swrCacheSeq atomic.Uint64

// mintSWRCacheName returns the next unique cache name in the tmdb_swr family.
// First call: "tmdb_swr". Subsequent: "tmdb_swr_1", "tmdb_swr_2", …
func mintSWRCacheName() string {
	n := swrCacheSeq.Add(1)
	if n == 1 {
		return "tmdb_swr"
	}
	return fmt.Sprintf("tmdb_swr_%d", n-1)
}

// swrCapacity is the LRU bound. Picked so the working set of a busy
// DiscoveryWorker + SVU compose + cold-start backfill fits without thrashing.
// Each entry holds a TMDB JSON body (worst case ~500 KiB for /tv/<id> with
// append_to_response). 256 entries × 500 KiB worst case = 128 MiB ceiling;
// median ~50 KiB → ~13 MiB typical. cachewatch's byte gauge surfaces actuals.
const swrCapacity = 256

// swrFetchTimeout bounds background refresh. context.Background() is the
// PARENT so caller cancellation never abandons a half-written refresh; the
// 30s ceiling caps stuck refreshes against TMDB ingress hangs (>30s would
// be caught by the operator's tmdb_limiter_wait_seconds histogram dashboards
// anyway, but defence-in-depth here is cheap).
const swrFetchTimeout = 30 * time.Second

// swrCache is the in-package wrapper. Constructed once per *Client at New.
// Fields:
//   - store:   byte-accounted LRU. Lifetime tied to *Client; Close() drains.
//   - pending: per-key first-claimer flag for refresh dedupe. A sync.Map of
//     cacheKey → struct{} populated via LoadOrStore. The FIRST stale-grace
//     caller for a key wins the slot and spawns the refresh goroutine;
//     subsequent callers see the slot occupied and skip the spawn entirely.
//     The goroutine clears the slot in its defer. Replaces the old
//     singleflight.Group: singleflight only dedupes IN-FLIGHT calls, so under
//     a slow scheduler (CI) the leader can return before late callers reach
//     DoChan, opening a window where multiple refreshes fire for the same
//     key. The LoadOrStore claim is atomic at the call site — no spawn-time
//     race. Story 553 (E-1 Z4) CI race fix.
//   - clk:     inherited from *Client for deterministic test boundary.
//   - fetch:   closure over the real Client.do — injected so swr_test.go can
//     replace it with a fake to assert on cache+TTL semantics without
//     an httptest.NewServer.
type swrCache struct {
	store   *cachewatch.Cache[string, []byte]
	pending sync.Map // map[string]struct{} — keys with an in-flight background refresh
	clk     clock.Clock

	// fetch is the underlying transport. In production it is Client.doDirect;
	// tests inject a fake that returns canned bodies + counts calls. The
	// signature mirrors doDirect exactly.
	fetch func(ctx context.Context, path string, query url.Values) ([]byte, error)

	// closeOnce ensures Close idempotency. Close()s the underlying cachewatch
	// (stops the reaper) so a Client.Close after-reload no longer leaks the
	// LRU's TTL reaper goroutine.
	closeOnce sync.Once

	// testHooks — non-nil ONLY in unit tests. Production code never sets
	// these. They sidestep two cross-package realities:
	//   (a) cachewatch.Add uses time.Now() internally (not the SWR clock),
	//       so a *clock.Fake on the SWR cache cannot move insertedAt;
	//   (b) the TTL tier table is a package-private constant and the test
	//       wants to exercise the stale-grace branch without waiting 28m.
	// `tierOverride` swaps the (ttl, label) for ALL paths in a test;
	// `insertedAtOverride` swaps the wall-clock insertedAt returned by
	// GetWithAge so the SWR clock-driven age math runs against test-controlled
	// timestamps. Both are read inside getRolling under no lock — tests must
	// only mutate before any getRolling call. Story 553 (E-1 Z4) test
	// infrastructure.
	tierOverride       func(path string) (time.Duration, string)
	insertedAtOverride func(key string) (time.Time, bool)

	// onRefreshDone — test-only hook fired AFTER the background refresh
	// goroutine has cleared its pending flag and is about to exit. Tests
	// that need to assert "the dedupe gate has reopened" hang a channel
	// signal here instead of polling, dodging the race where the test
	// observes f.fetch returning but the goroutine's defers (Delete of
	// pending, DecPending) have not yet executed. nil in production.
	// Story 553 (E-1 Z4) test infrastructure.
	onRefreshDone func(key string)
}

// newSWRCache constructs the wrapper. cacheName is the cachewatch register
// label — must be unique across the process. Production passes "tmdb_swr";
// tests pass a per-test name to dodge the cachewatch duplicate-registration
// panic (cachewatch.New panics on dup name).
//
// The store TTL is the LONGEST tier (24h) so the underlying reaper only
// catches the worst-case entries — the SWR wrapper itself decides per-tier
// freshness via resolveTier on every lookup. A `/discover/tv` entry (30m
// tier) will lazy-miss on the next call past 30m via age >= ttl in
// getRolling, well before the reaper's 24h sweep touches it. Keeping the
// reaper TTL long avoids accidental fast-tier eviction races.
func newSWRCache(cacheName string, clk clock.Clock, fetch func(context.Context, string, url.Values) ([]byte, error)) *swrCache {
	store := cachewatch.New[string, []byte](
		cacheName,
		swrCapacity,
		24*time.Hour, // reaper TTL — outer bound only
		func(_ string, v []byte) int { return len(v) },
	)
	return &swrCache{
		store: store,
		clk:   clk,
		fetch: fetch,
	}
}

// Close stops the reaper. Safe to call multiple times. After Close the
// wrapper MUST NOT be used; new calls would still hit the underlying LRU but
// the reaper goroutine is gone (acceptable — store is GC'd with the parent).
func (s *swrCache) Close() {
	s.closeOnce.Do(func() {
		_ = s.store.Close()
	})
}

// getRolling is the SWR entry point. Returns (body, error). On cache HIT
// (fresh OR stale-grace) the body is returned instantly. On cache MISS or
// hard EXPIRY it blocks on fetch.
//
// The returned []byte MUST be treated as read-only by the caller — it is
// shared with future cache hits. The TMDB endpoint methods immediately
// json.Unmarshal it into a fresh struct, so this is safe in practice.
func (s *swrCache) getRolling(ctx context.Context, path string, query url.Values) ([]byte, error) {
	key := cacheKey(path, query)
	var ttl time.Duration
	var tier string
	if s.tierOverride != nil {
		ttl, tier = s.tierOverride(path)
	} else {
		ttl, tier = resolveTier(path)
	}

	body, insertedAt, ok := s.store.GetWithAge(key)
	if !ok {
		// Cache miss — synchronous fetch.
		observability.IncTMDBSWRHit(tier, "miss")
		return s.syncFetch(ctx, path, query, key)
	}
	if s.insertedAtOverride != nil {
		if t, override := s.insertedAtOverride(key); override {
			insertedAt = t
		}
	}
	age := s.clk.Now().Sub(insertedAt)
	switch {
	case age < ttl-swrStaleGrace:
		// Fresh — return instantly.
		observability.IncTMDBSWRHit(tier, "fresh")
		return body, nil
	case age < ttl:
		// Stale grace window — return cached body INSTANTLY, kick a
		// background refresh under the per-key first-claimer gate.
		observability.IncTMDBSWRHit(tier, "stale")
		s.kickRefresh(path, query, key, tier)
		return body, nil
	default:
		// Hard expiry — treat as miss, synchronous fetch. Note: we do
		// NOT serve the expired body on fetch error (matches Overseerr).
		observability.IncTMDBSWRHit(tier, "expired")
		return s.syncFetch(ctx, path, query, key)
	}
}

// syncFetch hits the underlying transport synchronously and populates the
// cache on success. Errors propagate verbatim; the cache is NOT touched.
func (s *swrCache) syncFetch(ctx context.Context, path string, query url.Values, key string) ([]byte, error) {
	body, err := s.fetch(ctx, path, query)
	if err != nil {
		return nil, err
	}
	s.store.Add(key, body)
	return body, nil
}

// kickRefresh spawns a background refresh under a first-claimer-wins
// dedupe protocol. Concurrent stale-grace hits with the same key share ONE
// refresh: the FIRST caller's LoadOrStore returns alreadyPending=false and
// spawns the goroutine; subsequent callers see alreadyPending=true, tick
// the dedup counter, and return immediately without spawning. The goroutine
// clears the pending flag in its defer so the NEXT stale-grace window after
// completion can fire a fresh refresh.
//
// Why not singleflight: singleflight.Group only dedupes calls that arrive
// while the leader is in-flight. Under a slow scheduler (CI) the leader's
// fn can complete (and singleflight's internal key cleared) BEFORE late
// callers reach DoChan — they then become new leaders and fire duplicate
// upstream calls. The pre-spawn LoadOrStore claim closes that window: the
// dedupe decision is made BEFORE any goroutine spawn or scheduling slack.
//
// Goroutine context: context.Background() + swrFetchTimeout. The caller's
// ctx is intentionally NOT propagated — the caller is about to return the
// stale body and would cancel its ctx as soon as the HTTP response
// completes, killing our refresh.
//
// On success: store.Add overwrites with the fresh body (full TTL window
// reset since insertedAt is bumped by cachewatch.Add). On error: store is
// UNCHANGED. The next stale-grace hit retries; the existing entry stays
// serveable until it hits hard expiry.
func (s *swrCache) kickRefresh(path string, query url.Values, key, tier string) {
	// Atomic first-claimer wins: only the FIRST caller for this key spawns
	// the refresh goroutine. Subsequent callers see the pending flag and
	// tick the dedup counter. Story 553 (E-1 Z4) CI race fix.
	if _, alreadyPending := s.pending.LoadOrStore(key, struct{}{}); alreadyPending {
		s.store.RecordDedupHit()
		observability.IncTMDBSWRInflightDedup(tier)
		return
	}

	go func() {
		// Test hook fires LAST (declared first → runs last on LIFO defer
		// stack), AFTER the pending flag has been cleared and panic-recover
		// has settled. Production never sets onRefreshDone; the nil-check
		// is the gate.
		defer func() {
			if s.onRefreshDone != nil {
				s.onRefreshDone(key)
			}
		}()
		// Recover so a goroutine panic (malformed TMDB body, OOM on a
		// huge response) doesn't crash a worker. The reporter's
		// authReporter / quota tick already happen inside fetch on the
		// happy path.
		defer func() {
			if r := recover(); r != nil {
				observability.IncTMDBSWRRevalidate(tier, "panic")
			}
		}()
		// Clear the pending flag whether the fetch succeeds, errors, or
		// panics — so the next stale-grace hit can claim a fresh refresh
		// slot. Runs BEFORE the recover defer (LIFO), so the flag is
		// always cleared during normal exit, error return, or panic
		// unwind. The onRefreshDone hook (above, declared earliest)
		// fires AFTER this, guaranteeing tests observe the flag as
		// already cleared when the hook fires.
		defer s.pending.Delete(key)

		s.store.IncPending()
		defer s.store.DecPending()

		ctx, cancel := context.WithTimeout(context.Background(), swrFetchTimeout)
		defer cancel()
		body, err := s.fetch(ctx, path, query)
		if err != nil {
			observability.IncTMDBSWRRevalidate(tier, "error")
			return
		}
		s.store.Add(key, body)
		observability.IncTMDBSWRRevalidate(tier, "ok")
	}()
}

// cacheKey is the canonical string form of (path, query). Mirrors the URL
// builder inside Client.doDirect — keeping them in sync is a hard invariant:
// a drift here means cache hits diverge from the requests doDirect would
// issue, silently caching different content under the same key.
func cacheKey(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}
