package torrentsync

import (
	"maps"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Entry is the in-memory snapshot of a single torrent. It carries
// the rich qbit.TorrentInfo (live + persistent fields) plus two
// pieces of derived metadata the persist layer needs without
// re-reading qBit: SyncedAt (the wall clock of the Refresh that
// produced this entry) and a copy of the StateGroup hoisted to the
// top level so the diff path does not have to reach into
// Info.StateGroup on every comparison.
//
// Entries are value-types. The store hands out copies, never
// references, so consumers cannot mutate shared state through a
// returned Entry.
type Entry struct {
	Info       qbit.TorrentInfo
	StateGroup qbit.StateGroup
	SyncedAt   time.Time
	// LastFlushedCounters records the values of the mutable
	// counter set the last time the persist layer flushed this
	// row. The diff path compares against this to decide whether
	// the row needs to ride along on the next flush batch.
	// Zero-value (default) means "never flushed" — first sync
	// after a cold start treats every counter as dirty.
	LastFlushedCounters Counters
}

// Counters is the mutable-counter subset (PRD §4.6 "flush every
// 5 min batched"). Lives in the store so the diff path is a value
// comparison and the persist layer never needs to walk the full
// TorrentInfo. NB: `LastActivity` is included even though it is
// also a timestamp — qBit ticks it once per active second, so it
// behaves as a high-churn counter for our purposes.
type Counters struct {
	Ratio        float64
	Uploaded     int64
	TimeActiveS  int64
	SeedingTimeS int64
	Popularity   float64
	LastActivity time.Time
}

// CountersFrom projects a qbit.TorrentInfo into the Counters
// subset. Single point of truth for the field list so a future
// addition (e.g. `seen_complete`) lands here and nowhere else.
func CountersFrom(info qbit.TorrentInfo) Counters {
	return Counters{
		Ratio:        info.Ratio,
		Uploaded:     info.Uploaded,
		TimeActiveS:  int64(info.TimeActive / time.Second),
		SeedingTimeS: int64(info.SeedingTime / time.Second),
		Popularity:   info.Popularity,
		LastActivity: info.LastActivity,
	}
}

// Store is the per-instance in-memory inventory. Keyed by
// (instance, hash). Safe for concurrent use — the loop's Refresh
// goroutine writes, the HTTP handler (story 222) reads from a
// different goroutine.
//
// Memory budget: ~500 torrents per instance × ~1 KiB per Entry =
// 500 KiB per instance. Two-instance fleet fits comfortably in
// the existing pod's 256 MiB working set.
type Store struct {
	mu sync.RWMutex
	// rows is the primary index: instance → hash → Entry.
	rows map[domain.InstanceName]map[string]Entry
	// bySeries is the secondary index: instance →
	// sonarrSeriesID → set-of-hashes. Populated by the
	// reconciler (story 221) via SetSeriesMapping; consumed by
	// the read endpoint (story 222). Empty in 220 — the index
	// exists so 221 can wire writes without retro-fitting
	// the store shape.
	bySeries map[domain.InstanceName]map[domain.SonarrSeriesID]map[string]struct{}
}

// NewStore constructs an empty Store ready to receive an
// `EnsureInstance` per qBit-enabled Sonarr instance.
func NewStore() *Store {
	return &Store{
		rows:     make(map[domain.InstanceName]map[string]Entry),
		bySeries: make(map[domain.InstanceName]map[domain.SonarrSeriesID]map[string]struct{}),
	}
}

// EnsureInstance is idempotent. SwapSettings (cmd/server/
// torrentsync_loop.go) calls it on every reload publish so a
// newly-enabled instance gets its sub-map without racing the loop.
func (s *Store) EnsureInstance(instance domain.InstanceName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[instance]; !ok {
		s.rows[instance] = make(map[string]Entry)
	}
	if _, ok := s.bySeries[instance]; !ok {
		s.bySeries[instance] = make(map[domain.SonarrSeriesID]map[string]struct{})
	}
}

// DropInstance removes every entry for the named instance. Used
// when the operator disables qBit on an instance — the loop is
// already cancelled by then, so this call cannot race with a
// Refresh.
func (s *Store) DropInstance(instance domain.InstanceName) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, instance)
	delete(s.bySeries, instance)
}

// Get returns the current Entry for (instance, hash) and a boolean
// presence flag. The returned Entry is a value copy.
func (s *Store) Get(instance domain.InstanceName, hash string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.rows[instance]
	if !ok {
		return Entry{}, false
	}
	e, ok := inst[hash]
	return e, ok
}

// Put writes the Entry. Used by the loop on every Refresh tick.
// The store stamps SyncedAt if the caller left it zero — the loop
// always sets it explicitly so this is a defensive default.
func (s *Store) Put(instance domain.InstanceName, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.rows[instance]
	if !ok {
		inst = make(map[string]Entry)
		s.rows[instance] = inst
	}
	if e.SyncedAt.IsZero() {
		e.SyncedAt = time.Now().UTC()
	}
	inst[e.Info.Hash] = e
}

// Delete drops an entry. Called for hashes in
// Snapshot.Removed once the persist layer has stamped the row
// `present=false` — keeping the row in memory after we have told
// the DB it is gone would lie to the read endpoint.
func (s *Store) Delete(instance domain.InstanceName, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst, ok := s.rows[instance]; ok {
		delete(inst, hash)
	}
	if idx, ok := s.bySeries[instance]; ok {
		for seriesID, set := range idx {
			delete(set, hash)
			if len(set) == 0 {
				delete(idx, seriesID)
			}
		}
	}
}

// HashesFor returns the hashes currently mapped to the supplied
// series under instance. Empty slice on miss. The slice is a
// fresh copy — callers can mutate it without holding the store
// lock.
//
// Story 220 returns nothing here (the bySeries index is empty
// until 221 calls SetSeriesMapping). The accessor exists so 222's
// endpoint can read the store through one stable surface.
func (s *Store) HashesFor(instance domain.InstanceName, seriesID domain.SonarrSeriesID) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.bySeries[instance]
	if !ok {
		return nil
	}
	set, ok := idx[seriesID]
	if !ok || len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	return out
}

// SeriesForHash returns the sonarr series id mapped to the supplied
// hash, or 0 when no mapping exists. The reconciler uses this to
// decide whether a given hash is still "unmapped". Reverse-index
// over bySeries — O(seriesCount) per lookup, acceptable at <= 500
// series per instance.
func (s *Store) SeriesForHash(instance domain.InstanceName, hash string) domain.SonarrSeriesID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.bySeries[instance]
	if !ok {
		return 0
	}
	for seriesID, set := range idx {
		if _, ok := set[hash]; ok {
			return seriesID
		}
	}
	return 0
}

// SetSeriesMapping is the future hook for story 221's reconciler.
// Exposed here so the store shape is locked in 220. In 220 it is
// not called from any production path; the unit test in
// store_test.go exercises it to assert the secondary index
// behaves correctly.
func (s *Store) SetSeriesMapping(instance domain.InstanceName, hash string, seriesID domain.SonarrSeriesID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.bySeries[instance]
	if !ok {
		idx = make(map[domain.SonarrSeriesID]map[string]struct{})
		s.bySeries[instance] = idx
	}
	set, ok := idx[seriesID]
	if !ok {
		set = make(map[string]struct{})
		idx[seriesID] = set
	}
	set[hash] = struct{}{}
}

// All returns every (hash → Entry) tuple for one instance. Used
// by the test harness and the read endpoint's "list all torrents
// regardless of series" path. The returned map is a fresh copy.
func (s *Store) All(instance domain.InstanceName) map[string]Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.rows[instance]
	if !ok {
		return map[string]Entry{}
	}
	out := make(map[string]Entry, len(inst))
	maps.Copy(out, inst)
	return out
}

// Len returns the count of entries currently held for the named
// instance. Cheap accessor for metrics + tests.
func (s *Store) Len(instance domain.InstanceName) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows[instance])
}

// AllHashes returns the keyset of stored hashes for the instance.
// Returned map is safe to mutate — the store does not retain it.
// Used by the use case to detect newly-arriving torrents between
// ticks (B-32 newly-unmapped counter).
func (s *Store) AllHashes(instance domain.InstanceName) map[string]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, ok := s.rows[instance]
	if !ok {
		return nil
	}
	out := make(map[string]struct{}, len(rows))
	for h := range rows {
		out[h] = struct{}{}
	}
	return out
}
