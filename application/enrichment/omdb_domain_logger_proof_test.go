package enrichment

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNewOMDbBudgetGuard_StructLiteralDefault_EmitsDomainOMDb is the
// F-4b-5 proof for the "omdb" domain (Story 396): when
// NewOMDbBudgetGuard is called (it takes no logger argument — its
// struct literal at omdb_budget.go:75 unconditionally sets the
// logger field), the struct-literal default wraps slog.Default()
// via sharedports.DomainLogger(..., "omdb") so every record emitted
// by the guard carries domain="omdb".
//
// We construct the guard with the no-arg constructor to drive the
// struct-literal fallback path, then emit a deterministic record via
// the internal `logger` field (same-package test access). The captured
// buffer is asserted to contain `"domain":"omdb"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNewOMDbBudgetGuard_StructLiteralDefault_EmitsDomainOMDb(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// No-arg constructor unconditionally builds the guard with
	// logger: sharedports.DomainLogger(slog.Default(), "omdb") per
	// this story's patch. Initial=0 → defaults to DefaultOMDbBudget;
	// no DB counter wired (fallback path, exercising the struct
	// literal default).
	g := NewOMDbBudgetGuard(0)
	g.logger.WarnContext(context.Background(), "f4b5_omdb_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"omdb"`),
		"expected log record with domain=\"omdb\" when struct-literal default fires; got: %s", out)
}
