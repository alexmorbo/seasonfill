package sonarr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/series"
)

func sampleEpisodes(n int) []series.Episode {
	out := make([]series.Episode, n)
	for i := range n {
		out[i] = series.Episode{
			ID: i + 1, Number: i + 1, SeasonNumber: 1,
			Title: "ep", HasFile: i%2 == 0, AirDateUTC: time.Now().Add(-24 * time.Hour),
		}
	}
	return out
}

func TestEpisodesCache_GetMissReturnsFalse(t *testing.T) {
	t.Parallel()
	c := NewLRUEpisodesCache(1<<20, time.Hour)
	_, ok := c.Get("nope")
	assert.False(t, ok)
}

func TestEpisodesCache_PutThenGetHits(t *testing.T) {
	t.Parallel()
	c := NewLRUEpisodesCache(1<<20, time.Hour)
	eps := sampleEpisodes(3)
	c.Put("alpha:1", eps)
	got, ok := c.Get("alpha:1")
	require.True(t, ok)
	require.Len(t, got, 3)
	assert.Equal(t, 1, got[0].Number)
}

func TestEpisodesCache_PutOverwriteKeepsByteAccountingHonest(t *testing.T) {
	t.Parallel()
	c := NewLRUEpisodesCache(1<<20, time.Hour)
	c.Put("k", sampleEpisodes(10))
	c.Put("k", sampleEpisodes(3))
	got, ok := c.Get("k")
	require.True(t, ok)
	require.Len(t, got, 3)
	expected := int64(3*episodesPerEpisodeBytes + len("k") + episodesCacheEntryOverhead)
	assert.Equal(t, expected, c.TotalBytes())
}

func TestEpisodesCache_EvictsLRUTailWhenByteCapExceeded(t *testing.T) {
	t.Parallel()
	// 1 entry = 5 episodes * 160 + key + overhead. Pick a small cap.
	const epsPer = 5
	perEntry := int64(epsPer*episodesPerEpisodeBytes + 1 + episodesCacheEntryOverhead)
	cap := perEntry*2 + 50
	c := NewLRUEpisodesCache(cap, time.Hour)

	c.Put("a", sampleEpisodes(epsPer))
	c.Put("b", sampleEpisodes(epsPer))
	c.Put("c", sampleEpisodes(epsPer))

	_, okA := c.Get("a")
	_, okB := c.Get("b")
	_, okC := c.Get("c")
	assert.False(t, okA, "oldest entry must be evicted")
	assert.True(t, okB)
	assert.True(t, okC)
	assert.LessOrEqual(t, c.TotalBytes(), cap)
}

func TestEpisodesCache_RejectsOversizedEntry(t *testing.T) {
	t.Parallel()
	c := NewLRUEpisodesCache(100, time.Hour)
	c.Put("huge", sampleEpisodes(100))
	_, ok := c.Get("huge")
	assert.False(t, ok)
	assert.Equal(t, int64(0), c.TotalBytes())
}

func TestEpisodesCache_TTLExpiryDropsEntryOnNextGet(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: now}
	c := NewLRUEpisodesCache(1<<20, 5*time.Minute).withClock(clock.Now)

	c.Put("k", sampleEpisodes(3))
	clock.advance(2 * time.Minute)
	_, ok := c.Get("k")
	assert.True(t, ok, "2m < 5m TTL")

	clock.advance(4 * time.Minute) // total 6m > 5m
	_, ok = c.Get("k")
	assert.False(t, ok, "entry must expire after TTL")
	assert.Equal(t, int64(0), c.TotalBytes(), "expired entry releases its byte budget")
}

func TestEpisodesCache_NilLimitsFallBackToDefaults(t *testing.T) {
	t.Parallel()
	c := NewLRUEpisodesCache(0, 0)
	assert.NotNil(t, c)
	c.Put("k", sampleEpisodes(1))
	_, ok := c.Get("k")
	assert.True(t, ok)
}

func TestEpisodesCacheKey_StableShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "alpha:42", EpisodesCacheKey("alpha", 42))
	assert.Equal(t, "beta:7", EpisodesCacheKey("beta", 7))
}

// fakeClock is a manually-advanced time source for TTL tests.
type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }
