package sonarr

import (
	"crypto/sha1" // #nosec G505 — ETag synthesis only, NOT a security primitive.
	"encoding/hex"
	"strconv"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Default tuning for the in-process poster cache. The story pins
// these to keep tight RAM bounds while serving the whole frontend
// from cache after warm-up.
const (
	DefaultPosterCacheMaxBytes int64         = 256 << 20 // 256 MiB
	DefaultPosterCacheTTL      time.Duration = 24 * time.Hour
	// PosterCacheMaxEntries is the LRU's hard upper bound on entry
	// count. Byte-size accounting evicts long before this in normal
	// operation; the entry cap is a defensive ceiling against
	// pathological tiny-image floods.
	PosterCacheMaxEntries = 65536
	// posterCacheKeyOverhead approximates the per-entry bookkeeping
	// cost (key string, header bytes, LRU bucket). Added once per
	// entry to total bytes so eviction kicks in slightly early rather
	// than overshooting the configured cap.
	posterCacheKeyOverhead = 64
)

// PosterCacheEntry is the value stored in the LRU. Bytes is the
// already-decoded image payload (no streaming on cache hit). ContentType
// is forwarded verbatim from upstream. InsertedAt drives lazy TTL
// expiry — entries older than the configured TTL look like misses.
type PosterCacheEntry struct {
	Bytes       []byte
	ContentType string
	InsertedAt  time.Time
}

// PosterCache is the contract handlers consume. Get returns
// (entry, etag, ok) for a cache hit; the etag is synthesized
// off the key + insertion timestamp so it's stable for the
// lifetime of the entry but unique per (instance, series, size,
// generation). Put stores bytes + content-type.
type PosterCache interface {
	Get(key string) (PosterCacheEntry, string, bool)
	Put(key string, bytes []byte, contentType string)
}

// LRUPosterCache is a byte-capped LRU with lazy TTL expiry. The
// hashicorp/golang-lru/v2 store does the entry-count cap and recency
// tracking; we layer a sync.Mutex + size accountant on top to enforce
// the byte cap. On every Put we evict the LRU tail until the new entry
// fits under maxBytes.
type LRUPosterCache struct {
	mu        sync.Mutex
	store     *lru.Cache[string, PosterCacheEntry]
	maxBytes  int64
	ttl       time.Duration
	totalSize int64
	now       func() time.Time
}

// NewLRUPosterCache constructs an LRU with the given byte cap and TTL.
// maxBytes <= 0 falls back to DefaultPosterCacheMaxBytes; ttl <= 0 falls
// back to DefaultPosterCacheTTL. Construction never returns an error
// the caller would handle (the underlying lru.New rejects only entry
// cap <= 0 which we hard-code).
func NewLRUPosterCache(maxBytes int64, ttl time.Duration) *LRUPosterCache {
	if maxBytes <= 0 {
		maxBytes = DefaultPosterCacheMaxBytes
	}
	if ttl <= 0 {
		ttl = DefaultPosterCacheTTL
	}
	store, _ := lru.New[string, PosterCacheEntry](PosterCacheMaxEntries)
	return &LRUPosterCache{
		store:    store,
		maxBytes: maxBytes,
		ttl:      ttl,
		now:      time.Now,
	}
}

// withClock is a test hook for advancing time without sleeping.
func (c *LRUPosterCache) withClock(now func() time.Time) *LRUPosterCache {
	c.now = now
	return c
}

// Get returns the cached entry + synthesized ETag. Lazy TTL: an entry
// older than c.ttl is removed and reported as a miss. ETag is
// W/"<hex>" so callers can wire it straight to the response header.
func (c *LRUPosterCache) Get(key string) (PosterCacheEntry, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.store.Get(key)
	if !ok {
		return PosterCacheEntry{}, "", false
	}
	if c.ttl > 0 && c.now().Sub(entry.InsertedAt) > c.ttl {
		c.store.Remove(key)
		c.totalSize -= entrySize(key, entry)
		if c.totalSize < 0 {
			c.totalSize = 0
		}
		return PosterCacheEntry{}, "", false
	}
	return entry, SynthesizePosterETag(key, entry.InsertedAt), true
}

// Put stores bytes + content-type under key. Evicts LRU tails until
// the new entry fits under maxBytes. Entries larger than maxBytes by
// themselves are rejected silently (no point caching a single-entry
// blob that would evict everything else on the next Put). The fast
// path overwrites an existing key without changing totalSize math
// beyond the delta.
func (c *LRUPosterCache) Put(key string, bytes []byte, contentType string) {
	entry := PosterCacheEntry{
		Bytes:       bytes,
		ContentType: contentType,
		InsertedAt:  c.now(),
	}
	size := entrySize(key, entry)
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.store.Peek(key); ok {
		c.totalSize -= entrySize(key, old)
	}
	c.store.Add(key, entry)
	c.totalSize += size

	for c.totalSize > c.maxBytes {
		oldestKey, oldestEntry, ok := c.store.GetOldest()
		if !ok {
			break
		}
		c.store.Remove(oldestKey)
		c.totalSize -= entrySize(oldestKey, oldestEntry)
	}
	if c.totalSize < 0 {
		c.totalSize = 0
	}
}

// Len reports the number of live entries (test hook).
func (c *LRUPosterCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Len()
}

// TotalBytes reports the byte accountant (test hook).
func (c *LRUPosterCache) TotalBytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalSize
}

func entrySize(key string, e PosterCacheEntry) int64 {
	return int64(len(e.Bytes)) + int64(len(key)) + posterCacheKeyOverhead
}

// PosterCacheKey builds the cache key from instance + seriesID + size.
// Stable shape so test fixtures + handler share one canonical form.
func PosterCacheKey(instance string, seriesID int, size PosterSize) string {
	return instance + "/" + strconv.Itoa(seriesID) + "/" + string(size)
}

// SynthesizePosterETag derives a weak ETag from the cache key and the
// entry's insertion timestamp. sha1 is used solely as a fast,
// dependency-free fixed-length hash — NOT for collision resistance or
// security. Format: `W/"<40-hex>"`.
func SynthesizePosterETag(key string, insertedAt time.Time) string {
	h := sha1.New() // #nosec G401 — ETag-only.
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte("|"))
	_, _ = h.Write([]byte(insertedAt.UTC().Format(time.RFC3339Nano)))
	return `W/"` + hex.EncodeToString(h.Sum(nil)) + `"`
}
