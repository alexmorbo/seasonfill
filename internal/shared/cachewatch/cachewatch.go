// Package cachewatch is a thin wrapper around hashicorp/golang-lru/v2 that
// publishes the 7 cache metrics required by refactor PRD §6.7 and provides
// the BgFetcher primitives (IncPending / DecPending / RecordDedupHit)
// used by /discovery/discover (story 509) and other on-demand passthrough
// patterns. Every Cache instance auto-registers in a package-private
// singleton so `curl /metrics | grep '^cache_'` enumerates every live
// cache, and so duplicate-name instantiation panics at boot rather than
// silently double-registering.
//
// Lock order (single mutex): callers MUST NOT hold an external lock while
// calling Get/Add/Remove. The LRU library invokes our EvictCallback while
// it holds its internal lock — we never call back into the LRU from the
// callback to avoid re-entrancy.
package cachewatch

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"
	lru "github.com/hashicorp/golang-lru/v2"
)

// Sizer reports the approximate byte cost of a single (key, value) pair.
// Required (no default) per PRD §6.7 — callers know their payload shape
// better than the helper does, and silently picking a default would lie
// to `cache_bytes_estimated`.
type Sizer[K comparable, V any] func(K, V) int

// Cache is a generic byte-accounted LRU with optional TTL. Safe for
// concurrent use. Construct exactly one per logical cache name with New.
type Cache[K comparable, V any] struct {
	name     string
	capacity int
	ttl      time.Duration
	sizer    Sizer[K, V]

	mu    sync.Mutex
	store *lru.Cache[K, entry[V]]

	// bytes tracks the cumulative sizer output. Updated under mu only.
	bytes int64

	// pending is read by the metric publisher without holding mu so we
	// keep it atomic.
	pending atomic.Int64

	// evictReason disambiguates eviction sources for onEvicted. Mutated
	// only while holding mu, immediately before/after store mutations.
	evictReason evictReason

	// closeOnce + done stop the TTL reaper goroutine idempotently.
	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup

	// reason labels — pre-rendered once to avoid per-eviction allocation.
	gaugeEntries        *metrics.Gauge
	gaugeBytes          *metrics.Gauge
	gaugePendingFetches *metrics.Gauge
	counterHits         *metrics.Counter
	counterMisses       *metrics.Counter
	counterDedup        *metrics.Counter
	counterEvictCap     *metrics.Counter
	counterEvictTTL     *metrics.Counter
	counterEvictManual  *metrics.Counter
}

type entry[V any] struct {
	value      V
	insertedAt time.Time
	// size cached at Add time so eviction-callback (which only has key+entry)
	// can decrement bytes without re-running sizer (sizer needs both K and V
	// and we only have V in the callback — but more importantly, sizer may
	// be non-deterministic for slice values that mutated since Add).
	size int
}

// New constructs a Cache and registers it in the package singleton.
// Panics on bad arguments (caller bug, not runtime error):
//   - empty name
//   - capacity <= 0
//   - nil sizer
//   - duplicate name (already registered)
//
// ttl == 0 disables the reaper and lazy expiry — entries live until
// LRU-evicted by capacity or removed manually.
func New[K comparable, V any](name string, capacity int, ttl time.Duration, sizer Sizer[K, V]) *Cache[K, V] {
	if name == "" {
		panic("cachewatch.New: name must be non-empty")
	}
	if capacity <= 0 {
		panic(fmt.Sprintf("cachewatch.New[%s]: capacity must be > 0, got %d", name, capacity))
	}
	if sizer == nil {
		panic(fmt.Sprintf("cachewatch.New[%s]: sizer must be non-nil (PRD §6.7 requires caller-supplied byte estimator)", name))
	}

	c := &Cache[K, V]{
		name:     name,
		capacity: capacity,
		ttl:      ttl,
		sizer:    sizer,
		done:     make(chan struct{}),
	}

	// Wire the eviction callback BEFORE constructing the lru store so the
	// counter is bumped on every capacity eviction (the LRU lib invokes
	// the callback synchronously from Add).
	store, err := lru.NewWithEvict[K, entry[V]](capacity, func(k K, e entry[V]) {
		c.onEvicted(k, e)
	})
	if err != nil {
		// lru.NewWithEvict only errors on capacity <= 0, which we already guarded.
		panic(fmt.Sprintf("cachewatch.New[%s]: lru.NewWithEvict: %v", name, err))
	}
	c.store = store

	// Pre-create all metric handles. VictoriaMetrics interns by canonical
	// label string, so repeated GetOrCreate* on the same series returns
	// the same underlying counter — but doing it once at construction
	// removes a string-concat from the hot path.
	label := `cache="` + name + `"`
	c.gaugeEntries = metrics.GetOrCreateGauge(`cache_entries{`+label+`}`, nil)
	c.gaugeBytes = metrics.GetOrCreateGauge(`cache_bytes_estimated{`+label+`}`, nil)
	c.gaugePendingFetches = metrics.GetOrCreateGauge(`cache_pending_fetches{`+label+`}`, nil)
	c.counterHits = metrics.GetOrCreateCounter(`cache_hits_total{` + label + `}`)
	c.counterMisses = metrics.GetOrCreateCounter(`cache_misses_total{` + label + `}`)
	c.counterDedup = metrics.GetOrCreateCounter(`cache_dedup_hits_total{` + label + `}`)
	c.counterEvictCap = metrics.GetOrCreateCounter(`cache_evictions_total{` + label + `,reason="capacity"}`)
	c.counterEvictTTL = metrics.GetOrCreateCounter(`cache_evictions_total{` + label + `,reason="ttl"}`)
	c.counterEvictManual = metrics.GetOrCreateCounter(`cache_evictions_total{` + label + `,reason="manual"}`)

	// Initialise gauges so the series appears in /metrics even before the
	// first Add — operator dashboard alerts on cache_entries==0 need a
	// zero baseline.
	c.gaugeEntries.Set(0)
	c.gaugeBytes.Set(0)
	c.gaugePendingFetches.Set(0)

	registerOrPanic(name, c)

	if ttl > 0 {
		c.wg.Add(1)
		go c.reaper()
	}

	return c
}

// Get returns the value if present and not expired. Hits and misses are
// counted. Lazy TTL: an expired entry is removed in-line and reported as
// reason="ttl" eviction + miss.
func (c *Cache[K, V]) Get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.store.Peek(k)
	if !ok {
		c.counterMisses.Inc()
		var zero V
		return zero, false
	}
	if c.ttl > 0 && time.Since(e.insertedAt) > c.ttl {
		// Lazy expiry. Remove() inside store invokes our evict callback
		// which would bump reason="capacity" wrongly — so we suppress
		// the callback by setting evictReason before mutation.
		c.removeWithReason(k, reasonTTL)
		c.counterMisses.Inc()
		var zero V
		return zero, false
	}
	// Promote recency — Peek doesn't touch LRU order, Get does.
	_, _ = c.store.Get(k)
	c.counterHits.Inc()
	return e.value, true
}

// GetWithAge is the SWR variant of Get. Returns the value, the wall-clock
// timestamp at which the entry was inserted, and the presence flag. UNLIKE
// Get, GetWithAge does NOT lazy-evict on TTL boundary — the caller decides
// fresh-vs-stale based on its own age budget (computed against an injected
// clock if needed). This is required by the TMDB SWR wrapper which needs to
// RETURN stale values within the "stale grace window" and only evict on hard
// expiry.
//
// Hits and misses are still counted into cache_hits_total / cache_misses_total
// so operators see traffic in the same panels as Get. Recency is still promoted
// (the LRU order tracks "last accessed" — a heavily SWR-served entry stays
// warm). Story 553 (E-1 Z4).
func (c *Cache[K, V]) GetWithAge(k K) (V, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.store.Peek(k)
	if !ok {
		c.counterMisses.Inc()
		var zero V
		return zero, time.Time{}, false
	}
	// Promote recency — Peek doesn't touch LRU order, Get does.
	_, _ = c.store.Get(k)
	c.counterHits.Inc()
	return e.value, e.insertedAt, true
}

// Add inserts or replaces (k, v). Bumps cache_entries / cache_bytes_estimated.
// If insert would overflow capacity, the LRU evicts its tail (reason="capacity")
// via the callback wired in New.
func (c *Cache[K, V]) Add(k K, v V) {
	size := c.sizer(k, v)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.store.Peek(k); ok {
		// Replace — undo the old entry's accounting before the new
		// entry's callback-driven decrement collides with it.
		c.bytes -= int64(old.size)
		// suppressEvict so the in-place replace doesn't bump reason=capacity.
		c.evictReason = reasonReplace
	}
	c.store.Add(k, entry[V]{value: v, insertedAt: now, size: size})
	c.evictReason = reasonNone

	c.bytes += int64(size)
	c.gaugeEntries.Set(float64(c.store.Len()))
	c.gaugeBytes.Set(float64(c.bytes))
}

// Remove forcibly evicts (k). Bumps reason="manual" if the key was present.
func (c *Cache[K, V]) Remove(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeWithReason(k, reasonManual)
}

// Len returns the current entry count. Test hook.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Len()
}

// Close stops the TTL reaper. Idempotent. Safe to call multiple times.
// Metrics already published stay in the global VictoriaMetrics registry —
// that is by design (operators want post-mortem cache stats).
func (c *Cache[K, V]) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.wg.Wait()
	})
	return nil
}

// IncPending bumps cache_pending_fetches. Called by BgFetcher (story 509)
// when a background fetch starts.
func (c *Cache[K, V]) IncPending() {
	v := c.pending.Add(1)
	c.gaugePendingFetches.Set(float64(v))
}

// DecPending decrements cache_pending_fetches. Idempotent at zero
// (atomic clamps to >= 0).
func (c *Cache[K, V]) DecPending() {
	v := c.pending.Add(-1)
	if v < 0 {
		// Caller bug — but in production we'd rather clamp than panic.
		c.pending.Store(0)
		v = 0
	}
	c.gaugePendingFetches.Set(float64(v))
}

// RecordDedupHit ticks cache_dedup_hits_total. Used when a concurrent
// fetcher coalesces with an in-flight singleflight call.
func (c *Cache[K, V]) RecordDedupHit() {
	c.counterDedup.Inc()
}

// --- internals -------------------------------------------------------

type evictReason int

const (
	reasonNone evictReason = iota
	reasonCapacity
	reasonTTL
	reasonManual
	reasonReplace
)

// onEvicted is read inside the LRU's eviction callback to disambiguate
// eviction sources. Reset to reasonNone after each store mutation.
// Mutex-protected via mu (all callers hold c.mu).
func (c *Cache[K, V]) onEvicted(_ K, e entry[V]) {
	// Called under store's internal lock. We hold c.mu in all callers
	// (Add / Remove / removeWithReason), so reading c.evictReason and
	// adjusting c.bytes is safe.
	switch c.evictReason {
	case reasonReplace:
		// Old value being replaced — bytes already adjusted by Add.
		return
	case reasonManual, reasonTTL:
		// Counters bumped at the call site; bytes adjusted there too.
		return
	default:
		// Implicit capacity eviction by the LRU.
		c.bytes -= int64(e.size)
		c.counterEvictCap.Inc()
		c.gaugeBytes.Set(float64(c.bytes))
		c.gaugeEntries.Set(float64(c.store.Len()))
	}
}

// removeWithReason removes k and bumps the specified counter if the key
// was present. Caller MUST hold c.mu.
func (c *Cache[K, V]) removeWithReason(k K, reason evictReason) {
	e, ok := c.store.Peek(k)
	if !ok {
		return
	}
	c.evictReason = reason
	_ = c.store.Remove(k)
	c.evictReason = reasonNone

	c.bytes -= int64(e.size)
	if c.bytes < 0 {
		c.bytes = 0
	}
	switch reason {
	case reasonTTL:
		c.counterEvictTTL.Inc()
	case reasonManual:
		c.counterEvictManual.Inc()
	}
	c.gaugeEntries.Set(float64(c.store.Len()))
	c.gaugeBytes.Set(float64(c.bytes))
}

func (c *Cache[K, V]) reaper() {
	defer c.wg.Done()

	// Tick at min(ttl/4, 1m). Floor at 1s so unit tests with 50ms TTL
	// still get periodic sweeps without burning CPU.
	tick := max(min(c.ttl/4, time.Minute), time.Second)

	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.sweepExpired()
		}
	}
}

func (c *Cache[K, V]) sweepExpired() {
	cutoff := time.Now().Add(-c.ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Iterate snapshot of keys; we mutate the store inside the loop.
	keys := c.store.Keys()
	for _, k := range keys {
		e, ok := c.store.Peek(k)
		if !ok {
			continue
		}
		if e.insertedAt.Before(cutoff) {
			c.removeWithReason(k, reasonTTL)
		}
	}
}
