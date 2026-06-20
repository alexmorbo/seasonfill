package webhook

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNew_NilLogger_EmitsDomainWebhook is the F-4b-4 proof
// (Story 395): when webhook.New is called with Deps.Logger=nil, the
// fallback path wraps slog.Default() via sharedports.DomainLogger(...,
// "webhook") so every record emitted by this use case carries
// domain="webhook".
//
// We construct the UseCase with Logger=nil to drive the fallback path,
// then emit a deterministic record via the internal `logger` field
// (same-package test access). The captured buffer is asserted to
// contain `"domain":"webhook"`.
//
// NOT t.Parallel() — mutates slog.SetDefault (process-global state).
func TestNew_NilLogger_EmitsDomainWebhook(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	// Logger=nil drives the fallback path under test. All other Deps
	// fields are zero/nil — we never invoke Process, just emit a
	// record through the wired logger to prove the wrap.
	u := New(Deps{})
	u.logger.WarnContext(context.Background(), "f4b4_proof_emit")

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"webhook"`),
		"expected log record with domain=\"webhook\" when Logger=nil; got: %s", out)
}
