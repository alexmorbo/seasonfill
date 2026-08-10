// blocklist_cache.go holds the process-local discovery blocklist: two
// ref_id sets (tmdb series ids + TMDB keyword ids) loaded at boot and
// refreshed on every mutation. Readers hold a *BlocklistCache and call
// FilterBlocked at their output chokepoint; the canonical-hash builder
// (BE-keyword sibling) reads Epoch() + BlockedKeywordIDs().
//
// Thread-safety: an atomic snapshot pointer. Refresh builds a new
// immutable snapshot and swaps it, so readers never lock. Epoch is a
// separate atomic bumped on every successful Refresh so the discover LRU
// key can fold it (cache invalidation on mutate).
package app

import (
	"context"
	"sync/atomic"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// BlocklistLoader is the narrow read port the cache refreshes from.
// Satisfied structurally by *persistence.BlocklistRepository.
type BlocklistLoader interface {
	LoadBlockSets(ctx context.Context) (tmdbIDs []int64, keywordIDs []int64, err error)
}

// blocklistSnapshot is one immutable view of the blocklist. Never mutated
// after publish — Refresh builds a fresh one.
type blocklistSnapshot struct {
	tmdb    map[int64]struct{}
	keyword map[int64]struct{}
}

// BlocklistCache is a thread-safe, refresh-on-mutate view over
// discovery_blocklist. The zero value is NOT usable — construct via
// NewBlocklistCache. A nil *BlocklistCache is a valid no-op (FilterBlocked
// returns items unchanged), so readers can hold a nil cache in minimal
// wirings.
type BlocklistCache struct {
	loader BlocklistLoader
	snap   atomic.Pointer[blocklistSnapshot]
	epoch  atomic.Uint64
}

// NewBlocklistCache builds a cache seeded with an empty snapshot. Call
// Refresh(ctx) at boot to load the persisted sets.
func NewBlocklistCache(loader BlocklistLoader) *BlocklistCache {
	if loader == nil {
		panic("blocklist cache: loader required")
	}
	c := &BlocklistCache{loader: loader}
	c.snap.Store(&blocklistSnapshot{
		tmdb:    map[int64]struct{}{},
		keyword: map[int64]struct{}{},
	})
	return c
}

// Refresh reloads both sets from the loader and publishes a new snapshot,
// bumping the epoch. On loader error the previous snapshot is retained and
// the error is returned (readers keep working with stale-but-valid data).
func (c *BlocklistCache) Refresh(ctx context.Context) error {
	tmdbIDs, keywordIDs, err := c.loader.LoadBlockSets(ctx)
	if err != nil {
		return err
	}
	next := &blocklistSnapshot{
		tmdb:    make(map[int64]struct{}, len(tmdbIDs)),
		keyword: make(map[int64]struct{}, len(keywordIDs)),
	}
	for _, id := range tmdbIDs {
		next.tmdb[id] = struct{}{}
	}
	for _, id := range keywordIDs {
		next.keyword[id] = struct{}{}
	}
	c.snap.Store(next)
	c.epoch.Add(1)
	return nil
}

// Epoch returns the monotonically-increasing mutation counter. Bumped on
// every successful Refresh. The discover LRU key + canonical hash fold
// this so a blocklist mutation invalidates cached passthrough pages.
// Seam for the BE-keyword sibling story.
func (c *BlocklistCache) Epoch() uint64 {
	if c == nil {
		return 0
	}
	return c.epoch.Load()
}

// BlockedKeywordIDs returns a copy of the current keyword ref_id set as a
// slice. Seam for the BE-keyword sibling story (folds them into
// DiscoverFilter.WithoutKeywords). Returns nil when none.
func (c *BlocklistCache) BlockedKeywordIDs() []int64 {
	if c == nil {
		return nil
	}
	snap := c.snap.Load()
	if len(snap.keyword) == 0 {
		return nil
	}
	out := make([]int64, 0, len(snap.keyword))
	for id := range snap.keyword {
		out = append(out, id)
	}
	return out
}

// ApplyKeywordBlocklist returns filter with the blocked keyword ids merged
// into WithoutKeywords — the union of any caller-supplied WithoutKeywords
// and the current keyword blocklist, deduped, preserving the caller's ids
// first. The caller's filter is returned by value (its slices are not
// mutated). A nil cache or empty keyword set returns filter unchanged.
//
// Only discover-BACKED readers (the /discovery/discover passthrough + the
// worker by_genre/by_network/by_keyword fetches) can honour this — TMDB's
// trending/popular/search endpoints have no keyword parameter, so blocked
// keywords LEAK there (documented at each such call site + the smoke plan).
func (c *BlocklistCache) ApplyKeywordBlocklist(filter tmdb.DiscoverFilter) tmdb.DiscoverFilter {
	if c == nil {
		return filter
	}
	blocked := c.BlockedKeywordIDs()
	if len(blocked) == 0 {
		return filter
	}
	seen := make(map[int]struct{}, len(filter.WithoutKeywords)+len(blocked))
	out := make([]int, 0, len(filter.WithoutKeywords)+len(blocked))
	for _, k := range filter.WithoutKeywords {
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	for _, id := range blocked {
		k := int(id)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	filter.WithoutKeywords = out
	return filter
}

// IsBlockedTMDB reports whether tmdbID is in the blocked tmdb set.
func (c *BlocklistCache) IsBlockedTMDB(tmdbID int64) bool {
	if c == nil {
		return false
	}
	_, ok := c.snap.Load().tmdb[tmdbID]
	return ok
}

// FilterBlocked returns a NEW slice with every item whose TMDBID is in the
// blocked tmdb set removed. Items with a nil TMDBID (Sonarr-only stubs) are
// never blocked by kind=tmdb. A nil cache or empty blocklist returns items
// unchanged (same backing array — cheap passthrough). This is the SINGLE
// shared subtraction helper wired at every reader's output chokepoint.
func (c *BlocklistCache) FilterBlocked(items []disco.Item) []disco.Item {
	if c == nil || len(items) == 0 {
		return items
	}
	snap := c.snap.Load()
	if len(snap.tmdb) == 0 {
		return items
	}
	out := make([]disco.Item, 0, len(items))
	for _, it := range items {
		if it.TMDBID != nil {
			if _, blocked := snap.tmdb[int64(int(*it.TMDBID))]; blocked {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}
