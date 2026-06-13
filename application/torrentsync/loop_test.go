package torrentsync

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// fakeSession is a deterministic SyncSession that returns the
// stages a test queues in order. Refresh errors when stages is
// drained.
type fakeSession struct {
	mu     sync.Mutex
	stages []qbit.Snapshot
	errs   []error
	rid    int64
}

func (s *fakeSession) Refresh(_ context.Context) (qbit.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) > 0 {
		e := s.errs[0]
		s.errs = s.errs[1:]
		if e != nil {
			return qbit.Snapshot{}, e
		}
	}
	if len(s.stages) == 0 {
		return qbit.Snapshot{Rid: s.rid}, nil
	}
	snap := s.stages[0]
	s.stages = s.stages[1:]
	s.rid++
	snap.Rid = s.rid
	return snap, nil
}

func (s *fakeSession) Rid() int64 { return s.rid }

type fakeFactory struct{ sess *fakeSession }

func (f fakeFactory) NewSyncSession(_ context.Context, _ string) (qbit.SyncSession, error) {
	return f.sess, nil
}

func TestUseCase_RestartRecoveryPopulatesStore(t *testing.T) {
	repo := &fakeTorrentsRepo{
		listResp: []Entry{
			{
				Info: qbit.TorrentInfo{
					Hash:       "aaaa",
					Name:       "old show",
					StateGroup: qbit.StateGroupSeeding,
					DlSpeed:    9999, // should be zeroed
				},
				StateGroup: qbit.StateGroupSeeding,
			},
		},
	}
	events := &fakeEventsRepo{}
	store := NewStore()
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(store, policy, fakeFactory{sess: &fakeSession{}}, repo, slog.Default())

	require.NoError(t, uc.Hydrate(context.Background(), "alpha"))
	got, ok := store.Get("alpha", "aaaa")
	require.True(t, ok)
	assert.EqualValues(t, 0, got.Info.DlSpeed, "live fields must be zeroed on recovery")
	assert.Equal(t, "old show", got.Info.Name)
}

func TestUseCase_TorrentRemovedStampsAbsent(t *testing.T) {
	sess := &fakeSession{stages: []qbit.Snapshot{
		{Torrents: map[string]qbit.TorrentInfo{
			"aaaa": {Hash: "aaaa", StateGroup: qbit.StateGroupSeeding, StateRaw: "uploading"},
		}},
		{Torrents: map[string]qbit.TorrentInfo{}, Removed: []string{"aaaa"}},
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	store := NewStore()
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(store, policy, fakeFactory{sess: sess}, repo, slog.Default())

	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", now))
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", now.Add(30*time.Second)))

	assert.Equal(t, []string{"aaaa"}, repo.absent)
	_, ok := store.Get("alpha", "aaaa")
	assert.False(t, ok, "removed torrent must be evicted from memory")
}

func TestUseCase_CounterFlushCoalesces(t *testing.T) {
	// Three Refresh ticks within 5 min: counter changes accumulate.
	// Fourth tick crosses the 5m boundary → one BatchUpsert call.
	stage := func(uploaded int64) qbit.Snapshot {
		return qbit.Snapshot{Torrents: map[string]qbit.TorrentInfo{
			"aaaa": {
				Hash: "aaaa", Name: "show",
				StateGroup: qbit.StateGroupSeeding, StateRaw: "uploading",
				Uploaded: uploaded, Ratio: float64(uploaded) / 1000,
			},
		}}
	}
	sess := &fakeSession{stages: []qbit.Snapshot{
		stage(100), stage(200), stage(300), stage(400),
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	store := NewStore()
	policy := NewPersistPolicy(repo, events, slog.Default()).
		WithFlushInterval(5 * time.Minute)
	uc := NewUseCase(store, policy, fakeFactory{sess: sess}, repo, slog.Default())

	base := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	// First tick — `added`, one upsert + added event.
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base))
	// Next two ticks within 5m — no upsert/event, just pending.
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base.Add(time.Minute)))
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base.Add(2*time.Minute)))
	// Fourth tick crosses 5m → BatchUpsert fires once for the
	// accumulated counter delta.
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base.Add(6*time.Minute)))

	// Initial add already issued an Upsert; transitions did not
	// (state_group stayed `seeding`). The flush at minute 6 is
	// the first BatchUpsert call.
	require.Len(t, repo.batches, 1, "exactly one batch flush should fire")
	require.Len(t, repo.batches[0], 1, "one row in the batch (single torrent coalesced)")
}

func TestUseCase_SessionRebuildAfterRefreshError(t *testing.T) {
	sess := &fakeSession{
		errs:   []error{errors.New("boom")},
		stages: []qbit.Snapshot{{Torrents: map[string]qbit.TorrentInfo{}}},
	}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default())

	err := uc.RunInstance(context.Background(), "alpha", time.Now().UTC())
	require.Error(t, err)
	// Second tick rebuilds session and succeeds.
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", time.Now().UTC()))
}

func TestLoop_DegradesAfterThreeFailures(t *testing.T) {
	sess := &fakeSession{errs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default())

	l := NewLoop("alpha", uc, 30*time.Second, slog.Default())
	for i := 0; i < 3; i++ {
		l.iterate(context.Background())
	}
	assert.True(t, l.Degraded())
	assert.Equal(t, DegradedInterval, l.Interval())
}

func TestLoop_RecoversOnSuccess(t *testing.T) {
	sess := &fakeSession{
		errs:   []error{errors.New("e1"), errors.New("e2"), errors.New("e3"), nil},
		stages: []qbit.Snapshot{{Torrents: map[string]qbit.TorrentInfo{}}},
	}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default())

	l := NewLoop("alpha", uc, 30*time.Second, slog.Default())
	for i := 0; i < 3; i++ {
		l.iterate(context.Background())
	}
	require.True(t, l.Degraded())
	l.iterate(context.Background())
	assert.False(t, l.Degraded())
	assert.Equal(t, 30*time.Second, l.Interval())
}

func TestLoop_SetIntervalRespectsDegradedMode(t *testing.T) {
	sess := &fakeSession{errs: []error{errors.New("e"), errors.New("e"), errors.New("e")}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default())

	l := NewLoop("alpha", uc, 30*time.Second, slog.Default())
	for i := 0; i < 3; i++ {
		l.iterate(context.Background())
	}
	require.True(t, l.Degraded())

	// SetInterval during degraded window: configured cadence is
	// re-recorded but live interval stays at DegradedInterval.
	l.SetInterval(45 * time.Second)
	assert.Equal(t, DegradedInterval, l.Interval())
}

func TestLoop_RunExitsOnCtxCancel(t *testing.T) {
	sess := &fakeSession{stages: []qbit.Snapshot{
		{Torrents: map[string]qbit.TorrentInfo{}},
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default())

	l := NewLoop("alpha", uc, 10*time.Millisecond, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop.Run did not exit on ctx cancel")
	}
}
