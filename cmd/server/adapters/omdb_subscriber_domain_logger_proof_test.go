package adapters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewOMDbClientSubscriber_NilLogger_EmitsDomainOMDb is the F-4b-7
// proof for the "omdb" domain (Story 398): when NewOMDbClientSubscriber
// is called with logger=nil, the fallback path wraps slog.Default()
// via sharedports.DomainLogger(..., "omdb") so every record emitted
// by the subscriber carries domain="omdb".
//
// We construct the subscriber with logger=nil to drive the fallback
// path, then emit a deterministic record via the internal s.logger
// field (same-package test access). The captured buffer is asserted
// to contain `"domain":"omdb"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNewOMDbClientSubscriber_NilLogger_EmitsDomainOMDb(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	holder := NewOMDbClientHolder()
	sub := NewOMDbClientSubscriber(holder, nil)
	sub.logger.WarnContext(context.Background(), "f4b7_omdb_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"omdb"`),
		"expected log record with domain=\"omdb\" when logger=nil; got: %s", out)
}
