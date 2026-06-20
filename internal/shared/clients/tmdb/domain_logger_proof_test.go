package tmdb

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNew_NilLogger_EmitsDomainTMDB is the F-4b-7 proof for the
// "tmdb" domain (Story 398): when tmdb.New is called with
// Config{Logger: nil}, the fallback path wraps slog.Default() via
// sharedports.DomainLogger(..., "tmdb") so every record emitted by
// the client carries domain="tmdb".
//
// We construct the Client with Logger=nil to drive the fallback path,
// then emit a deterministic record via the internal c.logger field
// (same-package test access). The captured buffer is asserted to
// contain `"domain":"tmdb"`. We MUST Close the client to stop the
// rate-limiter refill goroutine.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNew_NilLogger_EmitsDomainTMDB(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	c, err := New(Config{
		Token:      "fixture-token",
		HTTPClient: &http.Client{},
		Logger:     nil,
	})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	c.logger.WarnContext(context.Background(), "f4b7_tmdb_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"tmdb"`),
		"expected log record with domain=\"tmdb\" when Logger=nil; got: %s", out)
}
