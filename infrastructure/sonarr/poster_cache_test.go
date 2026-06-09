package sonarr

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPosterCache_GetMissReturnsFalse(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(1<<20, time.Hour)
	_, _, ok := c.Get("nope")
	assert.False(t, ok)
}

func TestPosterCache_PutThenGetHits(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(1<<20, time.Hour)
	c.Put("k", []byte{1, 2, 3}, "image/jpeg")
	entry, etag, ok := c.Get("k")
	require.True(t, ok)
	assert.Equal(t, []byte{1, 2, 3}, entry.Bytes)
	assert.Equal(t, "image/jpeg", entry.ContentType)
	assert.True(t, strings.HasPrefix(etag, `W/"`))
	assert.True(t, strings.HasSuffix(etag, `"`))
}

func TestPosterCache_PutOverwriteKeepsByteAccountingHonest(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(1<<20, time.Hour)
	c.Put("k", make([]byte, 100), "image/jpeg")
	c.Put("k", make([]byte, 200), "image/png")
	entry, _, ok := c.Get("k")
	require.True(t, ok)
	assert.Equal(t, 200, len(entry.Bytes))
	assert.Equal(t, "image/png", entry.ContentType)
	// totalSize should match the live entry, not the sum of both Puts.
	expected := int64(200 + len("k") + posterCacheKeyOverhead)
	assert.Equal(t, expected, c.TotalBytes())
}

func TestPosterCache_EvictsLRUTailWhenByteCapExceeded(t *testing.T) {
	t.Parallel()
	// Cap small enough that 3 entries don't fit but 2 do.
	const payload = 400
	cap := int64(2*(payload+1+posterCacheKeyOverhead)) + 50
	c := NewLRUPosterCache(cap, time.Hour)

	c.Put("a", make([]byte, payload), "image/jpeg")
	c.Put("b", make([]byte, payload), "image/jpeg")
	c.Put("c", make([]byte, payload), "image/jpeg")

	_, _, okA := c.Get("a")
	_, _, okB := c.Get("b")
	_, _, okC := c.Get("c")
	assert.False(t, okA, "oldest entry must be evicted")
	assert.True(t, okB)
	assert.True(t, okC)
	assert.LessOrEqual(t, c.TotalBytes(), cap)
}

func TestPosterCache_RejectsOversizedEntry(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(100, time.Hour)
	c.Put("huge", make([]byte, 200), "image/jpeg")
	_, _, ok := c.Get("huge")
	assert.False(t, ok)
	assert.Equal(t, int64(0), c.TotalBytes())
}

func TestPosterCache_TTLExpiryDropsEntryOnNextGet(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}
	c := NewLRUPosterCache(1<<20, time.Hour).withClock(clock.Now)

	c.Put("k", []byte{1, 2, 3}, "image/jpeg")
	clock.advance(30 * time.Minute)
	_, _, ok := c.Get("k")
	assert.True(t, ok, "30m < 1h TTL")

	clock.advance(31 * time.Minute) // total 61m > 1h
	_, _, ok = c.Get("k")
	assert.False(t, ok, "entry must expire after TTL")
	assert.Equal(t, int64(0), c.TotalBytes(), "expired entry releases its byte budget")
}

func TestSynthesizePosterETag_DifferentKeysProduceDifferentETags(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a := SynthesizePosterETag("alpha/1/full", now)
	b := SynthesizePosterETag("alpha/2/full", now)
	assert.NotEqual(t, a, b)
}

func TestSynthesizePosterETag_StableAcrossCalls(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a := SynthesizePosterETag("alpha/1/full", now)
	b := SynthesizePosterETag("alpha/1/full", now)
	assert.Equal(t, a, b)
}

func TestPosterCacheKey_StableShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "alpha/42/full", PosterCacheKey("alpha", 42, PosterFull))
	assert.Equal(t, "beta/7/small", PosterCacheKey("beta", 7, PosterSmall))
}

func TestPosterCache_GetReturnsSameETagForSameEntry(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(1<<20, time.Hour)
	c.Put("k", []byte{1, 2}, "image/jpeg")
	_, etag1, _ := c.Get("k")
	_, etag2, _ := c.Get("k")
	assert.Equal(t, etag1, etag2, "ETag must be stable for the same cache entry")
}

func TestPosterCache_NilLimitsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	c := NewLRUPosterCache(0, 0)
	assert.NotNil(t, c)
	c.Put("k", []byte{1}, "image/jpeg")
	_, _, ok := c.Get("k")
	assert.True(t, ok)
}

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }
