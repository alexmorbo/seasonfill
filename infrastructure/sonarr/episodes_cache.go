package sonarr

import (
	"strconv"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Default tuning for the in-process episodes cache. Episodes are
// lightweight JSON (~150 B per episode) so a 32 MiB budget covers
// ~200k episodes — far above any realistic library. TTL 5 min keeps
// hot Sonarr data fresh enough that operator-driven /missing polls
// reflect new imports within the next refetch.
const (
	DefaultEpisodesCacheMaxBytes int64         = 32 << 20 // 32 MiB
	DefaultEpisodesCacheTTL      time.Duration = 5 * time.Minute
	// EpisodesCacheMaxEntries — defensive ceiling against pathological
	// floods of tiny series. Byte-size accounting evicts long before
	// this limit in normal operation.
	EpisodesCacheMaxEntries = 16384
	// episodesCacheEntryOverhead approximates per-entry bookkeeping
	// (key string, struct headers, LRU bucket). Added once per entry
	// to total bytes so eviction kicks in slightly early rather than
	// overshooting the configured cap. The per-episode payload itself
	// dominates (~150 B); this constant is a fixed-cost floor.
	episodesCacheEntryOverhead = 96
	// episodesPerEpisodeBytes is the conservative size estimate per
	// cached Episode. Used by entrySize to bound total bytes without
	// reflecting actual JSON encoding. Real Episode structs average
	// ~120 B; we round up to 160 B to absorb title-string variance.
	episodesPerEpisodeBytes = 160
)

// EpisodesCacheEntry holds a series' full episode list plus the
// insertion timestamp that drives lazy TTL expiry.
type EpisodesCacheEntry struct {
	Episodes   []series.Episode
	InsertedAt time.Time
}

// EpisodesCache is the contract the Missing handler consumes. Get
// returns (episodes, ok); Put stores the full series episode list.
// Implementations MUST be safe for concurrent use.
type EpisodesCache interface {
	Get(key string) ([]series.Episode, bool)
	Put(key string, episodes []series.Episode)
}

// LRUEpisodesCache is a byte-capped LRU with lazy TTL expiry.
// The hashicorp/golang-lru/v2 store does entry-count cap + recency
// tracking; sync.Mutex + size accountant on top enforce the byte cap.
// On every Put we evict the LRU tail until the new entry fits under
// maxBytes.
type LRUEpisodesCache struct {
	mu        sync.Mutex
	store     *lru.Cache[string, EpisodesCacheEntry]
	maxBytes  int64
	ttl       time.Duration
	totalSize int64
	now       func() time.Time
}

// NewLRUEpisodesCache constructs an LRU with the given byte cap and
// TTL. Non-positive arguments fall back to the package defaults.
func NewLRUEpisodesCache(maxBytes int64, ttl time.Duration) *LRUEpisodesCache {
	if maxBytes <= 0 {
		maxBytes = DefaultEpisodesCacheMaxBytes
	}
	if ttl <= 0 {
		ttl = DefaultEpisodesCacheTTL
	}
	store, _ := lru.New[string, EpisodesCacheEntry](EpisodesCacheMaxEntries)
	return &LRUEpisodesCache{
		store:    store,
		maxBytes: maxBytes,
		ttl:      ttl,
		now:      time.Now,
	}
}

// withClock is a test hook for advancing time without sleeping.
func (c *LRUEpisodesCache) withClock(now func() time.Time) *LRUEpisodesCache {
	c.now = now
	return c
}

// Get returns the cached episode list. Lazy TTL: an entry older than
// c.ttl is removed and reported as a miss.
func (c *LRUEpisodesCache) Get(key string) ([]series.Episode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.store.Get(key)
	if !ok {
		return nil, false
	}
	if c.ttl > 0 && c.now().Sub(entry.InsertedAt) > c.ttl {
		c.store.Remove(key)
		c.totalSize -= episodesEntrySize(key, entry)
		if c.totalSize < 0 {
			c.totalSize = 0
		}
		return nil, false
	}
	return entry.Episodes, true
}

// Put stores the episode list under key. Evicts LRU tails until the
// new entry fits under maxBytes. Entries larger than maxBytes on
// their own are rejected silently.
func (c *LRUEpisodesCache) Put(key string, episodes []series.Episode) {
	entry := EpisodesCacheEntry{
		Episodes:   episodes,
		InsertedAt: c.now(),
	}
	size := episodesEntrySize(key, entry)
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.store.Peek(key); ok {
		c.totalSize -= episodesEntrySize(key, old)
	}
	c.store.Add(key, entry)
	c.totalSize += size

	for c.totalSize > c.maxBytes {
		oldestKey, oldestEntry, ok := c.store.GetOldest()
		if !ok {
			break
		}
		c.store.Remove(oldestKey)
		c.totalSize -= episodesEntrySize(oldestKey, oldestEntry)
	}
	if c.totalSize < 0 {
		c.totalSize = 0
	}
}

// Len reports the number of live entries (test hook).
func (c *LRUEpisodesCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Len()
}

// TotalBytes reports the byte accountant (test hook).
func (c *LRUEpisodesCache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalSize
}

func episodesEntrySize(key string, e EpisodesCacheEntry) int64 {
	return int64(len(e.Episodes))*int64(episodesPerEpisodeBytes) +
		int64(len(key)) + int64(episodesCacheEntryOverhead)
}

// EpisodesCacheKey builds the cache key from instance + seriesID.
// Stable shape so test fixtures + handler share one canonical form.
func EpisodesCacheKey(instance domain.InstanceName, seriesID int) string {
	return string(instance) + ":" + strconv.Itoa(seriesID)
}
