package cachewatch

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stringSizer is the most common sizer used by tests — len(key)+len(value).
func stringSizer(k, v string) int { return len(k) + len(v) }

// uniqueName returns a UUID-derived cache name guaranteed not to collide
// across tests or across runs. VictoriaMetrics' default set is global
// and sticky, so per-test labels are non-negotiable.
func uniqueName(t *testing.T) string {
	t.Helper()
	return "test_" + uuid.NewString()
}

func dumpMetrics(t *testing.T) string {
	t.Helper()
	buf := &bytes.Buffer{}
	metrics.WritePrometheus(buf, true)
	return buf.String()
}

func TestCache_Add_IncrementsEntries(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 100, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	for i := range 10 {
		c.Add(fmt.Sprintf("k%d", i), "v")
	}

	assert.Equal(t, 10, c.Len())
	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_entries{cache="`+name+`"} 10`)
}

func TestCache_Get_Hit_BumpsHits(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("k", "v")
	v, ok := c.Get("k")
	require.True(t, ok)
	assert.Equal(t, "v", v)

	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_hits_total{cache="`+name+`"} 1`)
}

func TestCache_Get_Miss_BumpsMisses(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	_, ok := c.Get("absent")
	require.False(t, ok)

	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_misses_total{cache="`+name+`"} 1`)
}

func TestCache_Add_AtCapacity_BumpsCapacityEviction(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 3, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("a", "1")
	c.Add("b", "2")
	c.Add("c", "3")
	c.Add("d", "4") // forces eviction of "a"

	assert.Equal(t, 3, c.Len())
	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_evictions_total{cache="`+name+`",reason="capacity"} 1`)
}

func TestCache_TTL_LazyEviction_BumpsTTLEviction(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 10, 50*time.Millisecond, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("k", "v")
	time.Sleep(200 * time.Millisecond)

	_, ok := c.Get("k")
	require.False(t, ok)

	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_evictions_total{cache="`+name+`",reason="ttl"}`)
}

func TestCache_TTL_ReaperEviction_BumpsTTLEviction(t *testing.T) {
	name := uniqueName(t)
	// TTL 1s → reaper ticks every 1s (floor). Sleep 2.5s ensures at
	// least one sweep observes the expired entry.
	c := New[string, string](name, 10, time.Second, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("k", "v")
	time.Sleep(2500 * time.Millisecond)

	// No Get — the reaper must have done the work.
	assert.Equal(t, 0, c.Len())
	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_evictions_total{cache="`+name+`",reason="ttl"}`)
}

func TestCache_Remove_BumpsManualEviction(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("k", "v")
	c.Remove("k")
	c.Remove("absent") // no-op; must NOT bump the counter

	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_evictions_total{cache="`+name+`",reason="manual"} 1`)
}

func TestCache_BgFetcher_PendingAndDedup(t *testing.T) {
	name := uniqueName(t)
	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.IncPending()
	c.IncPending()
	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_pending_fetches{cache="`+name+`"} 2`)

	c.DecPending()
	body = dumpMetrics(t)
	assert.Contains(t, body, `cache_pending_fetches{cache="`+name+`"} 1`)

	c.RecordDedupHit()
	c.RecordDedupHit()
	c.RecordDedupHit()
	body = dumpMetrics(t)
	assert.Contains(t, body, `cache_dedup_hits_total{cache="`+name+`"} 3`)
}

func TestCache_Concurrent_RaceSafe(t *testing.T) {
	name := uniqueName(t)
	c := New[string, int](name, 1000, 0, func(k string, v int) int { return len(k) + 8 })
	t.Cleanup(func() { _ = c.Close() })

	const (
		goroutines = 100
		iterations = 100
	)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range iterations {
				k := fmt.Sprintf("g%d-i%d", g, i)
				c.Add(k, i)
				_, _ = c.Get(k)
				if i%10 == 0 {
					c.Remove(k)
				}
				c.IncPending()
				c.DecPending()
			}
		}(g)
	}
	wg.Wait()

	// Sanity: no panic, no negative bytes.
	body := dumpMetrics(t)
	assert.Contains(t, body, `cache_bytes_estimated{cache="`+name+`"}`)
}

func TestCache_Close_StopsReaper(t *testing.T) {
	// Baseline goroutine count BEFORE constructing any cache.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	for range 5 {
		c := New[string, string](uniqueName(t), 10, 100*time.Millisecond, stringSizer)
		_ = c.Close()
	}

	// Allow scheduler to retire goroutines that just exited their for-select.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()

	// Tolerance of 2 absorbs background goroutines from the test runtime.
	assert.LessOrEqual(t, after, before+2,
		"goroutine leak: before=%d after=%d (reaper not stopping on Close?)", before, after)
}

func TestCache_New_PanicsOnInvalidArgs(t *testing.T) {
	t.Run("empty name", func(t *testing.T) {
		assert.PanicsWithValue(t, "cachewatch.New: name must be non-empty", func() {
			_ = New[string, string]("", 10, 0, stringSizer)
		})
	})
	t.Run("zero capacity", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = New[string, string](uniqueName(t), 0, 0, stringSizer)
		})
	})
	t.Run("negative capacity", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = New[string, string](uniqueName(t), -1, 0, stringSizer)
		})
	})
	t.Run("nil sizer", func(t *testing.T) {
		assert.Panics(t, func() {
			_ = New[string, string](uniqueName(t), 10, 0, nil)
		})
	})
}

func TestRegistry_DuplicateName_Panics(t *testing.T) {
	name := uniqueName(t)
	c1 := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c1.Close() })

	assert.PanicsWithValue(t, fmt.Sprintf("cachewatch: cache %q is already registered (duplicate New)", name), func() {
		_ = New[string, string](name, 10, 0, stringSizer)
	})
}

func TestRegistry_NamesAndIsRegistered(t *testing.T) {
	name := uniqueName(t)
	require.False(t, IsRegistered(name))

	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	assert.True(t, IsRegistered(name))
	found := false
	for _, n := range Names() {
		if n == name {
			found = true
			break
		}
	}
	assert.True(t, found, "Names() must include the freshly registered cache")
}

func TestCache_Replace_DoesNotBumpCapacityEviction(t *testing.T) {
	// Regression: Add with an existing key replaces the value in-place.
	// The LRU library may invoke the evict callback for the displaced
	// entry — our wrapper MUST suppress reason="capacity" in that path.
	name := uniqueName(t)
	c := New[string, string](name, 10, 0, stringSizer)
	t.Cleanup(func() { _ = c.Close() })

	c.Add("k", "v1")
	c.Add("k", "v2") // replace, NOT capacity-evict

	body := dumpMetrics(t)
	// Counter MAY be present at 0 (gauge-init style) but MUST NOT show 1.
	assert.NotContains(t, body, `cache_evictions_total{cache="`+name+`",reason="capacity"} 1`)
}

// hush the unused-import linter when running with strings only in places.
var _ = strings.Contains
