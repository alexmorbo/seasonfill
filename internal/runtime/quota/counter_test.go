package quota

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryCounter_Increment_StartsAtOne(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	n, err := c.Increment(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
}

func TestInMemoryCounter_Increment_Accumulates(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	for i := 1; i <= 5; i++ {
		n, err := c.Increment(context.Background(), "omdb", w)
		require.NoError(t, err)
		assert.Equal(t, i, n)
	}
}

func TestInMemoryCounter_DistinctServices_DistinctRows(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	_, _ = c.Increment(context.Background(), "omdb", w)
	_, _ = c.Increment(context.Background(), "omdb", w)
	_, _ = c.Increment(context.Background(), "tmdb", w)

	o, _ := c.Get(context.Background(), "omdb", w)
	tm, _ := c.Get(context.Background(), "tmdb", w)
	assert.Equal(t, 2, o)
	assert.Equal(t, 1, tm)
}

func TestInMemoryCounter_DistinctWindows_DistinctRows(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w1 := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	w2 := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	_, _ = c.Increment(context.Background(), "omdb", w1)
	_, _ = c.Increment(context.Background(), "omdb", w1)
	_, _ = c.Increment(context.Background(), "omdb", w2)

	g1, _ := c.Get(context.Background(), "omdb", w1)
	g2, _ := c.Get(context.Background(), "omdb", w2)
	assert.Equal(t, 2, g1)
	assert.Equal(t, 1, g2)
}

func TestInMemoryCounter_Get_MissingRow_ReturnsZero(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	n, err := c.Get(context.Background(), "omdb", w)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "missing row reads as zero, not error")
}

func TestInMemoryCounter_Reset_DeletesOldWindows(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	old := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	cur := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	_, _ = c.Increment(context.Background(), "omdb", old)
	_, _ = c.Increment(context.Background(), "omdb", mid)
	_, _ = c.Increment(context.Background(), "omdb", cur)

	// Cutoff = mid — strictly-before semantics keep mid itself.
	deleted, err := c.Reset(context.Background(), mid)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted, "only `old` is strictly before mid")

	gOld, _ := c.Get(context.Background(), "omdb", old)
	gMid, _ := c.Get(context.Background(), "omdb", mid)
	gCur, _ := c.Get(context.Background(), "omdb", cur)
	assert.Equal(t, 0, gOld)
	assert.Equal(t, 1, gMid)
	assert.Equal(t, 1, gCur)
}

func TestInMemoryCounter_ConcurrentIncrement_NoLost(t *testing.T) {
	t.Parallel()
	c := NewInMemoryCounter(nil)
	w := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)

	const goroutines = 16
	const tries = 50
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range tries {
				_, _ = c.Increment(context.Background(), "omdb", w)
			}
		})
	}
	wg.Wait()
	n, _ := c.Get(context.Background(), "omdb", w)
	assert.Equal(t, goroutines*tries, n, "no lost updates under contention")
}

// Compile-time assertion that InMemoryCounter satisfies the port.
var _ QuotaCounter = (*InMemoryCounter)(nil)
