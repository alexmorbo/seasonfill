package torrentsync

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// TestPersistPolicy_NilLogger_EmitsDomainQbit is the F-4b-2 proof
// (Story 393): when NewPersistPolicy is called with logger=nil, the
// fallback path wraps slog.Default() via sharedports.DomainLogger(..., "qbit")
// so every record emitted by this policy carries domain="qbit".
//
// HandleTransition with prev=nil emits exactly one INFO record
// ("torrentsync_added") which is sufficient as a deterministic
// emission anchor.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestPersistPolicy_NilLogger_EmitsDomainQbit(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	repo := &fakeTorrentsRepo{}
	events := &fakeEventsRepo{}
	// Logger=nil drives the fallback path under test.
	policy := NewPersistPolicy(repo, events, nil)

	next := Entry{
		Info:       qbit.TorrentInfo{Hash: "aaaa", StateRaw: "downloading"},
		StateGroup: qbit.StateGroupDownloading,
	}
	persisted, err := policy.HandleTransition(context.Background(), "alpha", nil, next)
	require.NoError(t, err)
	assert.True(t, persisted)

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"qbit"`),
		"expected log record with domain=\"qbit\" when logger=nil; got: %s", out)
}
