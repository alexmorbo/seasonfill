package regrab

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUseCase_NilLogger_EmitsDomainWatchdog is the F-4b-3 proof
// (Story 394): when NewUseCase is called with logger=nil, the
// fallback path wraps slog.Default() via sharedports.DomainLogger(...,
// "watchdog") so every record emitted by this use case carries
// domain="watchdog".
//
// We construct the UseCase with logger=nil to drive the fallback path,
// then emit a deterministic record via the internal `logger` field
// (same-package test access). The captured buffer is asserted to
// contain `"domain":"watchdog"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestUseCase_NilLogger_EmitsDomainWatchdog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// Logger=nil drives the fallback path under test.
	// All non-logger dependencies are nil — we never invoke RunInstance,
	// just emit a record through the wired logger to prove the wrap.
	u := NewUseCase(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	u.logger.WarnContext(context.Background(), "f4b3_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"watchdog"`),
		"expected log record with domain=\"watchdog\" when logger=nil; got: %s", out)
}
