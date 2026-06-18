package ports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

func TestFromContext_NilContext(t *testing.T) {
	t.Parallel()
	rc := ports.FromContext(nil) //nolint:staticcheck // intentional nil-ctx defensive path
	assert.Nil(t, rc.Logger)
	assert.Equal(t, "", rc.TraceID)
}

func TestFromContext_BackgroundNoValue(t *testing.T) {
	t.Parallel()
	rc := ports.FromContext(context.Background())
	assert.Nil(t, rc.Logger)
	assert.Equal(t, "", rc.TraceID)
}

func TestWithRequestContext_RoundTrip(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))

	ctx := ports.WithRequestContext(context.Background(), ports.RequestContext{
		Logger:  log,
		TraceID: "t-1",
	})
	rc := ports.FromContext(ctx)
	assert.Same(t, log, rc.Logger)
	assert.Equal(t, "t-1", rc.TraceID)
}

func TestWithRequestContext_OverwriteLastWins(t *testing.T) {
	t.Parallel()
	first := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	second := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	ctx := ports.WithRequestContext(context.Background(), ports.RequestContext{Logger: first, TraceID: "first"})
	ctx = ports.WithRequestContext(ctx, ports.RequestContext{Logger: second, TraceID: "second"})

	rc := ports.FromContext(ctx)
	assert.Same(t, second, rc.Logger)
	assert.Equal(t, "second", rc.TraceID)
}

func TestLoggerFromContext_Empty(t *testing.T) {
	t.Parallel()
	got := ports.LoggerFromContext(context.Background())
	require.NotNil(t, got)
	// Must not panic when used.
	assert.NotPanics(t, func() { got.Info("smoke") })
}

func TestLoggerFromContext_Present(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))

	ctx := ports.WithRequestContext(context.Background(), ports.RequestContext{Logger: log, TraceID: "t-2"})
	got := ports.LoggerFromContext(ctx)
	require.NotNil(t, got)
	got.Info("hello", slog.String("k", "v"))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "hello", entry["msg"])
	assert.Equal(t, "v", entry["k"])
}

func TestLoggerFromContext_NilContextDoesNotPanic(t *testing.T) {
	t.Parallel()
	var got *slog.Logger
	assert.NotPanics(t, func() {
		got = ports.LoggerFromContext(nil) //nolint:staticcheck // intentional nil-ctx defensive path
	})
	require.NotNil(t, got)
	assert.Same(t, slog.Default(), got)
}

func TestLoggerFromContext_ConcurrentContextsDoNotLeak(t *testing.T) {
	t.Parallel()
	const n = 32
	loggers := make([]*slog.Logger, n)
	contexts := make([]context.Context, n)
	for i := 0; i < n; i++ {
		l := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
		loggers[i] = l
		contexts[i] = ports.WithRequestContext(context.Background(), ports.RequestContext{
			Logger:  l,
			TraceID: "t",
		})
	}
	for i := 0; i < n; i++ {
		assert.Same(t, loggers[i], ports.FromContext(contexts[i]).Logger)
	}
}
