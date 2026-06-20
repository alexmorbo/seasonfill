package gc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPruner records the cutoff argument and returns the configured
// (deleted, skipped, skipReason, err) tuple. Mirrors the
// fakeEventsRepo pattern in application/torrentsync/persist_test.go.
type stubPruner struct {
	deleted    int
	skipped    bool
	skipReason string
	err        error
	gotCutoff  time.Time
	called     int
}

func (s *stubPruner) PruneOlderThan(_ context.Context, cutoff time.Time) (int, bool, string, error) {
	s.called++
	s.gotCutoff = cutoff
	return s.deleted, s.skipped, s.skipReason, s.err
}

func TestEventPrune_NilRepoSkipsRepoNotConfigured(t *testing.T) {
	t.Parallel()
	build := EventPruneDeps{}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, "repo_not_configured", res.SkipReason)
	assert.Equal(t, 0, res.Deleted)
}

func TestEventPrune_RepoSkipPassesThrough(t *testing.T) {
	t.Parallel()
	stub := &stubPruner{skipped: true, skipReason: "table_not_present_pending_a3"}
	build := EventPruneDeps{Repo: stub}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, "table_not_present_pending_a3", res.SkipReason)
	assert.Equal(t, 0, res.Deleted)
	assert.Equal(t, 1, stub.called)
}

func TestEventPrune_RepoErrorBubbles(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	stub := &stubPruner{err: want}
	build := EventPruneDeps{Repo: stub}.Build()
	res, err := build(context.Background())
	require.ErrorIs(t, err, want)
	assert.False(t, res.Skipped)
	assert.Equal(t, 0, res.Deleted)
}

func TestEventPrune_RepoSuccessLogsAndReturnsDeleted(t *testing.T) {
	t.Parallel()
	stub := &stubPruner{deleted: 7}
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	build := EventPruneDeps{
		Repo:   stub,
		Logger: logger,
		Clock:  func() time.Time { return now },
	}.Build()

	res, err := build(context.Background())
	require.NoError(t, err)
	assert.False(t, res.Skipped)
	assert.Equal(t, 7, res.Deleted)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &payload))
	assert.Equal(t, "event_prune.deleted", payload["msg"])
	assert.EqualValues(t, 7, payload["rows"])
	require.Contains(t, payload, "cutoff")
}

func TestEventPrune_DefaultRetention180Days(t *testing.T) {
	t.Parallel()
	stub := &stubPruner{}
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	build := EventPruneDeps{
		Repo:  stub,
		Clock: func() time.Time { return now },
	}.Build()
	_, err := build(context.Background())
	require.NoError(t, err)
	wantCutoff := now.Add(-180 * 24 * time.Hour)
	assert.True(t, stub.gotCutoff.Equal(wantCutoff),
		"want cutoff %s, got %s", wantCutoff, stub.gotCutoff)
}
