package seriesdetail

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewComposer_NilLogger_EmitsDomainComposer is the F-4b-6 proof
// for the "composer" domain (Story 397): when NewComposer is called
// with Deps{Logger: nil}, the fallback path wraps slog.Default() via
// sharedports.DomainLogger(..., "composer") so every record emitted
// by the composer carries domain="composer".
//
// We construct the Composer with Logger=nil to drive the fallback
// path, then emit a deterministic record via the internal d.Logger
// field (same-package test access). The captured buffer is asserted
// to contain `"domain":"composer"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNewComposer_NilLogger_EmitsDomainComposer(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// Logger=nil drives the fallback path under test. Deps is otherwise
	// empty — we never call Get/GetSeason, just emit a record through
	// the wired logger to prove the wrap.
	c := NewComposer(Deps{})
	c.d.Logger.WarnContext(context.Background(), "f4b6_composer_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"composer"`),
		"expected log record with domain=\"composer\" when Logger=nil; got: %s", out)
}
