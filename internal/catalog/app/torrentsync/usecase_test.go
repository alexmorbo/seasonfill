package torrentsync

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubTorrentsyncMetrics records every adapter call so a test can
// assert deltas / state distribution / last-refresh / fresh-unmapped
// without touching the global VictoriaMetrics registry.
type stubTorrentsyncMetrics struct {
	durations        []stubDuration
	stateCounts      map[qbit.StateGroup]int
	deltas           map[string]int
	lastRefresh      int64
	unmappedDetected int
}

type stubDuration struct {
	outcome string
	seconds float64
}

func newStubTorrentsyncMetrics() *stubTorrentsyncMetrics {
	return &stubTorrentsyncMetrics{
		stateCounts: map[qbit.StateGroup]int{},
		deltas:      map[string]int{},
	}
}

func (s *stubTorrentsyncMetrics) ObserveRefreshDuration(_ domain.InstanceName, outcome string, seconds float64) {
	s.durations = append(s.durations, stubDuration{outcome: outcome, seconds: seconds})
}

func (s *stubTorrentsyncMetrics) SetTorrentsByState(_ domain.InstanceName, state qbit.StateGroup, count int) {
	s.stateCounts[state] = count
}

func (s *stubTorrentsyncMetrics) AddDelta(_ domain.InstanceName, op string, n int) {
	s.deltas[op] += n
}

func (s *stubTorrentsyncMetrics) SetLastRefreshAt(_ domain.InstanceName, unixSec int64) {
	s.lastRefresh = unixSec
}

func (s *stubTorrentsyncMetrics) AddUnmappedDetected(_ domain.InstanceName, n int) {
	s.unmappedDetected += n
}

// TestUseCase_RunInstance_EmitsB32Metrics asserts the use case
// captures the right per-tick telemetry: insert delta == fresh
// torrents, state distribution reflects the live store, last-
// refresh stamps `now`, and unmapped counter increments by the
// count of fresh hashes that were not previously in the store.
func TestUseCase_RunInstance_EmitsB32Metrics(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{stages: []qbit.Snapshot{
		{Torrents: map[string]qbit.TorrentInfo{
			"h1": {Hash: "h1", StateGroup: qbit.StateGroupDownloading, StateRaw: "downloading"},
			"h2": {Hash: "h2", StateGroup: qbit.StateGroupSeeding, StateRaw: "uploading"},
			"h3": {Hash: "h3", StateGroup: qbit.StateGroupStalled, StateRaw: "stalledDL"},
		}},
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	stub := newStubTorrentsyncMetrics()
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default()).WithMetrics(stub)

	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", now))

	assert.Equal(t, 3, stub.deltas["insert"], "all three torrents are fresh inserts")
	assert.Equal(t, 0, stub.deltas["update"], "no updates on a cold tick")
	assert.Equal(t, 0, stub.deltas["delete"], "no removals reported")
	assert.Equal(t, 1, stub.stateCounts[qbit.StateGroupDownloading])
	assert.Equal(t, 1, stub.stateCounts[qbit.StateGroupSeeding])
	assert.Equal(t, 1, stub.stateCounts[qbit.StateGroupStalled])
	assert.Equal(t, 0, stub.stateCounts[qbit.StateGroupPaused], "uninhabited states emit zero")
	assert.Equal(t, now.Unix(), stub.lastRefresh)
	assert.Equal(t, 3, stub.unmappedDetected, "all three hashes are fresh arrivals")
}

// TestUseCase_RunInstance_EmitsDeleteDelta asserts the delete op
// counter increments by len(snap.Removed) — the operator wants the
// qBit-reported delta, not the count of successful MarkAbsent calls.
func TestUseCase_RunInstance_EmitsDeleteDelta(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{stages: []qbit.Snapshot{
		{Torrents: map[string]qbit.TorrentInfo{
			"h1": {Hash: "h1", StateGroup: qbit.StateGroupSeeding, StateRaw: "uploading"},
		}},
		{Torrents: map[string]qbit.TorrentInfo{}, Removed: []string{"h1"}},
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	stub := newStubTorrentsyncMetrics()
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default()).WithMetrics(stub)

	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base))
	// Reset per-tick accumulators so the second tick's assertions
	// reflect only that tick's contribution.
	stub.deltas = map[string]int{}
	stub.unmappedDetected = 0
	require.NoError(t, uc.RunInstance(context.Background(), "alpha", base.Add(30*time.Second)))

	assert.Equal(t, 1, stub.deltas["delete"], "qBit reported one removal")
	assert.Equal(t, 0, stub.unmappedDetected, "no fresh hashes in the second tick")
}

// TestUseCase_WithMetrics_NilRestoresNullMetrics asserts WithMetrics(nil)
// reinstates the no-op default and subsequent RunInstance calls do not
// panic.
func TestUseCase_WithMetrics_NilRestoresNullMetrics(t *testing.T) {
	t.Parallel()
	sess := &fakeSession{stages: []qbit.Snapshot{
		{Torrents: map[string]qbit.TorrentInfo{}},
	}}
	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	policy := newPolicy(t, repo, events)
	uc := NewUseCase(NewStore(), policy, fakeFactory{sess: sess}, repo, slog.Default()).
		WithMetrics(newStubTorrentsyncMetrics()).
		WithMetrics(nil)

	require.NoError(t, uc.RunInstance(context.Background(), "alpha", time.Now().UTC()))
}
