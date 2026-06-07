package regrab

import (
	"sync"
	"time"
)

// PollResult is the canonical string set used as LastPollResult.
// Mirrors the existing observability poll-result label without
// re-importing internal/observability.
const (
	PollResultOK        = "ok"
	PollResultQbitError = "qbit_error"
	PollResultSkipped   = "skipped"
)

// RuntimeState is the in-memory bookkeeping the regrab use case stamps
// at the end of every RunInstance call. Watched is the post-category
// filter torrent count from the most recent successful list-torrents
// call; preserved across failures so a transient qBit blip does not
// flip the UI gauge to zero.
type RuntimeState struct {
	LastPollAt     time.Time
	LastPollResult string
	QbitReachable  bool
	Watched        int
}

// RuntimeStateStore is a goroutine-safe per-instance bookkeeping map.
type RuntimeStateStore struct {
	mu    sync.RWMutex
	byKey map[string]RuntimeState
}

func NewRuntimeStateStore() *RuntimeStateStore {
	return &RuntimeStateStore{byKey: make(map[string]RuntimeState)}
}

// Stamp overwrites the row for instance.
func (s *RuntimeStateStore) Stamp(instance string, st RuntimeState) {
	if instance == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[instance] = st
}

// StampPartial overwrites LastPollAt/LastPollResult/QbitReachable and
// updates Watched only when result == PollResultOK AND watched >= 0.
// Negative watched + non-OK result preserves the prior value.
func (s *RuntimeStateStore) StampPartial(instance string, at time.Time, result string, reachable bool, watched int) {
	if instance == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.byKey[instance]
	cur := RuntimeState{
		LastPollAt:     at,
		LastPollResult: result,
		QbitReachable:  reachable,
		Watched:        prev.Watched,
	}
	if result == PollResultOK && watched >= 0 {
		cur.Watched = watched
	}
	s.byKey[instance] = cur
}

// Snapshot returns (state, true) when stamped at least once.
func (s *RuntimeStateStore) Snapshot(instance string) (RuntimeState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.byKey[instance]
	return st, ok
}

// SnapshotAll returns a shallow clone of the map.
func (s *RuntimeStateStore) SnapshotAll() map[string]RuntimeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]RuntimeState, len(s.byKey))
	for k, v := range s.byKey {
		out[k] = v
	}
	return out
}

// Forget drops an instance — called by the instance CRUD delete path.
func (s *RuntimeStateStore) Forget(instance string) {
	if instance == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, instance)
}
