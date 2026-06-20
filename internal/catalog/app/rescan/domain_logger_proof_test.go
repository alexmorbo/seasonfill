package rescan

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUseCase_NilLogger_EmitsDomainScan is the F-4b-3 proof
// (Story 394, rescan slice): when NewUseCase is called with logger=nil,
// the fallback path wraps slog.Default() via sharedports.DomainLogger
// (..., "scan") so every record emitted by this use case carries
// domain="scan".
//
// Same-package access reads the unexported `logger` field after
// construction; we emit a deterministic record on it and assert the
// captured buffer contains `"domain":"scan"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestUseCase_NilLogger_EmitsDomainScan(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// Logger=nil drives the fallback path under test.
	// All non-logger dependencies are nil — we never invoke Start,
	// just emit a record through the wired logger to prove the wrap.
	u := NewUseCase(nil, nil, nil, nil, nil, nil, nil)
	u.logger.WarnContext(context.Background(), "f4b3_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"scan"`),
		"expected log record with domain=\"scan\" when logger=nil; got: %s", out)
}
