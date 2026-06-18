package enrichment

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewDispatcher_NilLogger_EmitsDomainEnrichment is the F-4b-5 proof
// for the "enrichment" domain (Story 396): when NewDispatcher is called
// with logger=nil, the fallback path wraps slog.Default() via
// sharedports.DomainLogger(..., "enrichment") so every record emitted
// by the dispatcher carries domain="enrichment".
//
// We construct the DispatcherImpl with logger=nil to drive the fallback
// path, then emit a deterministic record via the internal `logger`
// field (same-package test access). The captured buffer is asserted to
// contain `"domain":"enrichment"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNewDispatcher_NilLogger_EmitsDomainEnrichment(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// logger=nil drives the fallback path under test. Workers is
	// empty — we never call Start, just emit a record through the
	// wired logger to prove the wrap.
	d := NewDispatcher(Workers{}, nil)
	d.logger.WarnContext(context.Background(), "f4b5_enrichment_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"enrichment"`),
		"expected log record with domain=\"enrichment\" when logger=nil; got: %s", out)
}
