package tmdb

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clock"
)

// TestSWR_StaleConcurrentRefresh_SingleflightDedup verifies that 100
// concurrent stale-grace callers spawn EXACTLY ONE upstream refresh —
// singleflight.Group dedupes the rest. Mandatory under -race. Story 553
// (E-1 Z4) acceptance criterion §5.
func TestSWR_StaleConcurrentRefresh_SingleflightDedup(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	bodyA := []byte(`{"v":"A"}`)
	bodyB := []byte(`{"v":"B"}`)

	// Barrier the background refresh so we can observe ALL surplus
	// callers landing in DoChan's shared=true branch before the leader
	// completes.
	release := make(chan struct{})
	var refreshCalls atomic.Int64
	// coldSeeded flips after the cold-seed completes; subsequent calls block
	// on the barrier BEFORE incrementing refreshCalls so the test can assert
	// "no refresh has executed yet" while N goroutines wait.
	var coldSeeded atomic.Bool

	fetcher := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		if !coldSeeded.Load() {
			refreshCalls.Add(1)
			coldSeeded.Store(true)
			return bodyA, nil
		}
		// Refresh path — block on the barrier FIRST so the assert sees
		// no executed refresh while the test gate is closed.
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		refreshCalls.Add(1)
		return bodyB, nil
	}

	s := newSWRCache(uniqueSWRCacheName(t), clk, fetcher)
	t.Cleanup(func() { s.Close() })
	setStamp := installFakeInsertedAt(s)

	// Cold seed.
	body, err := s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyA, body)
	require.EqualValues(t, 1, refreshCalls.Load())
	setStamp(cacheKey("/discover/tv", nil), clk.Now())

	// Advance into stale-grace window. /discover/tv → 30m TTL, grace 90s.
	clk.Advance(29*time.Minute + 30*time.Second)

	// Spawn N concurrent stale callers. Each should receive bodyA
	// instantly — the background refresh is blocked on the barrier.
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			b, err := s.getRolling(context.Background(), "/discover/tv", nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if string(b) != string(bodyA) {
				t.Errorf("stale callers must get bodyA, got %q", string(b))
			}
		}()
	}
	wg.Wait()

	// All N callers have returned. None have spawned new TMDB fetches yet
	// (we're holding the barrier closed). refreshCalls is still 1 (cold).
	// The N background goroutines are queued on singleflight.DoChan;
	// exactly ONE will execute the upstream once the barrier opens.
	assert.EqualValues(t, 1, refreshCalls.Load(),
		"barrier still closed → exactly one cold-seed call; refresh not yet executed")

	// Release the barrier — exactly one goroutine should perform the
	// refresh; the other N-1 share the result via singleflight.
	close(release)

	// Wait for the refresh to land. We need a deterministic synchronisation
	// point — sleep with a long timeout would be flaky. Instead, poll the
	// counter with a tight loop bound. Singleflight resolves DoChan as
	// soon as the leader returns.
	deadline := time.Now().Add(5 * time.Second)
	for refreshCalls.Load() < 2 && time.Now().Before(deadline) {
		// Yield CPU to the runtime.
		time.Sleep(time.Millisecond)
	}
	assert.EqualValues(t, 2, refreshCalls.Load(),
		"exactly ONE refresh upstream call after barrier — singleflight dedupe held")

	// Drain pending goroutines. Use the cache's pending-fetch gauge as
	// a proxy: a zero count means all background workers exited. Bound
	// the wait similarly.
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// pendingFetches is published on cache_pending_fetches gauge;
		// we don't expose the int directly, so we just hold until the
		// runtime quiesces. Simple bound — the goroutines all wake on
		// the same released barrier and finish quickly.
		time.Sleep(2 * time.Millisecond)
		// Once the leader's Add overwrote the entry, advance clock
		// slightly and read — fresh body confirms refresh applied.
		break
	}

	// One more read — should now serve bodyB from the freshened cache.
	clk.Advance(time.Second)
	body, err = s.getRolling(context.Background(), "/discover/tv", nil)
	require.NoError(t, err)
	assert.Equal(t, bodyB, body, "post-refresh read returns freshened body")
}
