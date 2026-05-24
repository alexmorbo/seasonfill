package auth

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestIPLimiter_Allow_PerKey(t *testing.T) {
	t.Parallel()
	lim := NewIPLimiter(rate.Every(time.Hour), 2)
	assert.True(t, lim.Allow("ip-a"))
	assert.True(t, lim.Allow("ip-a"))
	assert.False(t, lim.Allow("ip-a"), "burst exhausted")
	assert.True(t, lim.Allow("ip-b"), "separate key has own bucket")
}

// TestIPLimiter_EvictionUnderStress validates HIGH-S3: rotating
// distinct keys past IPLimiterMaxEntries must not unbounded-grow the
// map. With idle entries older than IdleTTL, the prune drops them as
// new ones come in.
func TestIPLimiter_EvictionUnderStress(t *testing.T) {
	t.Parallel()
	lim := NewIPLimiter(rate.Every(time.Hour), 1)
	now := time.Now()
	lim.SetClock(func() time.Time { return now })

	// Fill to the cap.
	for i := 0; i < IPLimiterMaxEntries; i++ {
		lim.Allow("k-" + strconv.Itoa(i))
	}
	require.Equal(t, IPLimiterMaxEntries, lim.Len())

	// Advance past IdleTTL so prune can reclaim everything.
	now = now.Add(IPLimiterIdleTTL + time.Minute)
	// Inserting one new key triggers pruneLocked (len >= cap).
	lim.Allow("fresh")
	// All stale entries should be gone; only the fresh one remains.
	assert.Equal(t, 1, lim.Len())
}

func TestIPLimiter_NoEvictionWithinTTL(t *testing.T) {
	t.Parallel()
	lim := NewIPLimiter(rate.Every(time.Hour), 1)
	now := time.Now()
	lim.SetClock(func() time.Time { return now })
	for i := 0; i < IPLimiterMaxEntries; i++ {
		lim.Allow("k-" + strconv.Itoa(i))
	}
	// Within TTL — prune drops nothing. New key still slots in; map
	// grows by one. This is acceptable: prune is best-effort, the
	// invariant is "won't unbounded-grow over time".
	now = now.Add(time.Minute)
	lim.Allow("fresh")
	assert.Equal(t, IPLimiterMaxEntries+1, lim.Len())
}

func TestIPLimiter_ConcurrentAllow(t *testing.T) {
	t.Parallel()
	lim := NewIPLimiter(rate.Every(time.Millisecond), 10)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lim.Allow("k-" + strconv.Itoa(i%5))
		}(i)
	}
	wg.Wait()
	assert.LessOrEqual(t, lim.Len(), 5)
}
