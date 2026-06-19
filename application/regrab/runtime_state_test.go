package regrab

import (
	"sync"
	"testing"
	"time"
)

func TestRuntimeStateStore_StampAndSnapshot(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	now := time.Date(2026, 6, 7, 1, 30, 0, 0, time.UTC)
	s.Stamp("alpha", RuntimeState{LastPollAt: now, LastPollResult: PollResultOK, QbitReachable: true, Watched: 12})

	got, ok := s.Snapshot("alpha")
	if !ok {
		t.Fatal("expected snapshot for stamped instance")
	}
	if got.Watched != 12 || got.LastPollResult != PollResultOK || !got.LastPollAt.Equal(now) || !got.QbitReachable {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestRuntimeStateStore_SnapshotMissReturnsFalse(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	if _, ok := s.Snapshot("nope"); ok {
		t.Fatal("expected miss")
	}
}

func TestRuntimeStateStore_StampPartialPreservesWatchedOnFailure(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	t0 := time.Date(2026, 6, 7, 1, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Minute)
	s.StampPartial("alpha", t0, PollResultOK, true, 8)
	s.StampPartial("alpha", t1, PollResultQbitError, false, -1)

	got, _ := s.Snapshot("alpha")
	if got.Watched != 8 {
		t.Errorf("Watched preservation: got %d want 8", got.Watched)
	}
	if got.QbitReachable || got.LastPollResult != PollResultQbitError || !got.LastPollAt.Equal(t1) {
		t.Errorf("partial overwrite wrong: %+v", got)
	}
}

func TestRuntimeStateStore_StampPartialOverwritesOnOK(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	t0 := time.Date(2026, 6, 7, 1, 0, 0, 0, time.UTC)
	s.StampPartial("alpha", t0, PollResultOK, true, 5)
	s.StampPartial("alpha", t0.Add(time.Hour), PollResultOK, true, 9)
	got, _ := s.Snapshot("alpha")
	if got.Watched != 9 {
		t.Errorf("Watched: got %d want 9", got.Watched)
	}
}

func TestRuntimeStateStore_SnapshotAllReturnsCopy(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	s.Stamp("alpha", RuntimeState{Watched: 1})
	s.Stamp("beta", RuntimeState{Watched: 2})
	all := s.SnapshotAll()
	if len(all) != 2 {
		t.Fatalf("len: %d", len(all))
	}
	delete(all, "alpha")
	if _, ok := s.Snapshot("alpha"); !ok {
		t.Error("Snapshot affected by external mutation")
	}
}

func TestRuntimeStateStore_Forget(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	s.Stamp("alpha", RuntimeState{Watched: 1})
	s.Forget("alpha")
	if _, ok := s.Snapshot("alpha"); ok {
		t.Fatal("Forget did not remove the entry")
	}
}

func TestRuntimeStateStore_EmptyInstanceIsNoOp(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	s.Stamp("", RuntimeState{Watched: 9})
	s.StampPartial("", time.Now(), PollResultOK, true, 5)
	s.Forget("")
	if len(s.SnapshotAll()) != 0 {
		t.Error("empty instance must not populate the store")
	}
}

func TestRuntimeStateStore_ConcurrentStamps(t *testing.T) {
	t.Parallel()
	s := NewRuntimeStateStore()
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.StampPartial("alpha", time.Now(), PollResultOK, true, i)
		}(i)
	}
	wg.Wait()
	if _, ok := s.Snapshot("alpha"); !ok {
		t.Fatal("expected snapshot after concurrent stamps")
	}
}
