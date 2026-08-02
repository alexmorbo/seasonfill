package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithTraceID_And_TraceID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assert.Empty(t, TraceID(ctx))

	ctx = WithTraceID(ctx, "abc-123")
	assert.Equal(t, "abc-123", TraceID(ctx))
}

func TestTraceID_EmptyOnFreshContext(t *testing.T) {
	t.Parallel()
	assert.Empty(t, TraceID(context.Background()))
}

// CRITICAL: trace_id flows from context into emitted log records.
func TestNew_JSON_PropagatesTraceIDFromContext(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "debug", Format: "json", Output: buf})

	ctx := WithTraceID(context.Background(), "trace-42")
	lg.InfoContext(ctx, "hello")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "trace-42", entry["trace_id"])
	assert.Equal(t, "hello", entry["msg"])
}

// REGRESSION: a derived logger (.With) must keep injecting trace_id. Without a
// re-wrapping WithAttrs on contextHandler, logger.With(...) unwraps the context
// handler and trace_id silently stops flowing.
func TestNew_JSON_DerivedLogger_KeepsTraceID_PlainHandler(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "debug", Format: "json", Output: buf})

	derived := lg.With(slog.String("domain", "webhook"))
	derived.InfoContext(WithTraceID(context.Background(), "trace-99"), "hi")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "trace-99", entry["trace_id"])
	assert.Equal(t, "webhook", entry["domain"])
	assert.Equal(t, "hi", entry["msg"])
}

// REGRESSION: same guarantee on the real production path where a
// DomainLevelHandler wraps the contextHandler. The re-wrap must survive both the
// outer DomainLevelHandler.WithAttrs promotion AND the inner contextHandler
// WithAttrs re-wrap so trace_id keeps flowing through a DomainLogger.
func TestNew_JSON_DerivedLogger_KeepsTraceID_DomainLevelHandler(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{
		Level:        "debug",
		Format:       "json",
		Output:       buf,
		DomainLevels: map[string]slog.Level{"webhook": slog.LevelDebug},
	})

	derived := lg.With(slog.String("domain", "webhook"))
	derived.InfoContext(WithTraceID(context.Background(), "trace-99"), "hi")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "trace-99", entry["trace_id"])
	assert.Equal(t, "webhook", entry["domain"])
	assert.Equal(t, "hi", entry["msg"])
}

func TestNew_JSON_NoTraceID_NoFieldAdded(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "debug", Format: "json", Output: buf})

	lg.Info("no-context-trace")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	_, present := entry["trace_id"]
	assert.False(t, present, "trace_id must not appear when no context value is set")
}

func TestNew_Text_HandlerFormat(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "info", Format: "text", Output: buf})
	lg.Info("text-message", slog.String("key", "value"))

	out := buf.String()
	assert.Contains(t, out, "text-message")
	assert.Contains(t, out, "key=value")
}

func TestNew_RespectsLevel(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "warn", Format: "json", Output: buf})

	lg.Debug("debug-msg")
	lg.Info("info-msg")
	lg.Warn("warn-msg")

	out := buf.String()
	assert.NotContains(t, out, "debug-msg")
	assert.NotContains(t, out, "info-msg")
	assert.Contains(t, out, "warn-msg")
}

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"WARN":    slog.LevelWarn,
		"error":   slog.LevelError,
		"ERROR":   slog.LevelError,
		"":        slog.LevelInfo,
		"garbage": slog.LevelInfo,
	}

	for in, want := range tests {
		assert.Equal(t, want, parseLevel(in), "for input %q", in)
	}
}

func TestNew_TimeFormat_IsRFC3339Nano_UTC(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	lg := New(Config{Level: "info", Format: "json", Output: buf})
	lg.Info("ts-check")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))

	ts, ok := entry["time"].(string)
	require.True(t, ok, "time must be a string")
	// RFC3339Nano UTC ends with "Z".
	assert.Contains(t, ts, "T")
	assert.Contains(t, ts, "Z")
}
