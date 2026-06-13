package torrentsync

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// fakeTorrentsRepo records every call so tests can assert
// upsert + batch + mark-absent semantics without an sqlite
// round-trip.
type fakeTorrentsRepo struct {
	mu       sync.Mutex
	upserts  []Entry
	batches  [][]Entry
	absent   []string
	listResp []Entry
	listErr  error
	upsErr   error
	batchErr error
}

func (f *fakeTorrentsRepo) Upsert(_ context.Context, _ string, e Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsErr != nil {
		return f.upsErr
	}
	f.upserts = append(f.upserts, e)
	return nil
}

func (f *fakeTorrentsRepo) BatchUpsert(_ context.Context, _ string, entries []Entry, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.batchErr != nil {
		return f.batchErr
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeTorrentsRepo) MarkAbsent(_ context.Context, _ string, hash string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.absent = append(f.absent, hash)
	return nil
}

func (f *fakeTorrentsRepo) List(_ context.Context, _ string) ([]Entry, error) {
	return f.listResp, f.listErr
}

type fakeEventsRepo struct {
	mu     sync.Mutex
	events []EventRow
	err    error
}

func (f *fakeEventsRepo) Insert(_ context.Context, row EventRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, row)
	return nil
}

func newPolicy(t *testing.T, repo *fakeTorrentsRepo, events *fakeEventsRepo) *PersistPolicy {
	t.Helper()
	return NewPersistPolicy(repo, events, slog.Default()).
		WithClock(func() time.Time { return time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC) })
}

func TestPersist_AddedEmitsUpsertAndAddedEvent(t *testing.T) {
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	p := newPolicy(t, repo, events)

	next := Entry{
		Info:       qbit.TorrentInfo{Hash: "aaaa", StateRaw: "downloading"},
		StateGroup: qbit.StateGroupDownloading,
	}
	persisted, err := p.HandleTransition(context.Background(), "alpha", nil, next)
	require.NoError(t, err)
	assert.True(t, persisted)
	require.Len(t, repo.upserts, 1)
	require.Len(t, events.events, 1)
	assert.Equal(t, EventAdded, events.events[0].Event)
}

func TestPersist_StateTransitionEmitsOneEvent(t *testing.T) {
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	p := newPolicy(t, repo, events)

	prev := Entry{StateGroup: qbit.StateGroupDownloading,
		Info: qbit.TorrentInfo{Hash: "aaaa", StateGroup: qbit.StateGroupDownloading}}
	next := Entry{
		StateGroup: qbit.StateGroupSeeding,
		Info: qbit.TorrentInfo{
			Hash:       "aaaa",
			StateGroup: qbit.StateGroupSeeding,
		},
	}
	persisted, err := p.HandleTransition(context.Background(), "alpha", &prev, next)
	require.NoError(t, err)
	assert.True(t, persisted)
	require.Len(t, repo.upserts, 1)
	// One state_change + one completed (first time entering seeding).
	require.Len(t, events.events, 2)
	assert.Equal(t, EventStateChange, events.events[0].Event)
	assert.Equal(t, qbit.StateGroupDownloading, events.events[0].From)
	assert.Equal(t, qbit.StateGroupSeeding, events.events[0].To)
	assert.Equal(t, EventCompleted, events.events[1].Event)
}

func TestPersist_NoTransitionNoWrite(t *testing.T) {
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	p := newPolicy(t, repo, events)

	prev := Entry{StateGroup: qbit.StateGroupSeeding,
		Info: qbit.TorrentInfo{Hash: "aaaa", StateGroup: qbit.StateGroupSeeding}}
	next := Entry{StateGroup: qbit.StateGroupSeeding,
		Info: qbit.TorrentInfo{Hash: "aaaa", StateGroup: qbit.StateGroupSeeding}}
	persisted, err := p.HandleTransition(context.Background(), "alpha", &prev, next)
	require.NoError(t, err)
	assert.False(t, persisted)
	assert.Empty(t, repo.upserts)
	assert.Empty(t, events.events)
}

func TestPersist_HandleRemovalStampsAbsentAndEmitsDeleted(t *testing.T) {
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	p := newPolicy(t, repo, events)

	require.NoError(t, p.HandleRemoval(context.Background(), "alpha", "aaaa"))
	assert.Equal(t, []string{"aaaa"}, repo.absent)
	require.Len(t, events.events, 1)
	assert.Equal(t, EventDeleted, events.events[0].Event)
}

func TestPersist_FlushCountersBatchesAll(t *testing.T) {
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	p := newPolicy(t, repo, events)

	pending := []Entry{
		{Info: qbit.TorrentInfo{Hash: "a"}},
		{Info: qbit.TorrentInfo{Hash: "b"}},
		{Info: qbit.TorrentInfo{Hash: "c"}},
	}
	require.NoError(t, p.FlushCounters(context.Background(), "alpha", pending))
	require.Len(t, repo.batches, 1)
	assert.Len(t, repo.batches[0], 3)
}
