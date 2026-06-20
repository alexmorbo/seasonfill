package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newJSONLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// readLastJSONLine returns the parsed last non-empty JSON line written to buf.
func readLastJSONLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.NotEmpty(t, lines, "expected at least one log line")
	last := lines[len(lines)-1]
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(last), &entry), "raw: %s", last)
	return entry
}

func TestRequestLoggerMiddleware_GeneratesTraceID_WhenAbsent(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/ping", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	headerID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, headerID)
	_, err := uuid.Parse(headerID)
	require.NoError(t, err, "X-Request-ID must be a valid uuid: %q", headerID)

	entry := readLastJSONLine(t, buf)
	assert.Equal(t, "http_request", entry["msg"])
	assert.Equal(t, "http", entry["domain"])
	assert.Equal(t, headerID, entry["trace_id"])

	httpGroup, ok := entry["http"].(map[string]any)
	require.True(t, ok, "expected http group in log entry: %+v", entry)
	assert.Equal(t, "GET", httpGroup["method"])
	assert.Equal(t, "/ping", httpGroup["path"])
	assert.Equal(t, float64(http.StatusOK), httpGroup["status"])
	assert.Equal(t, headerID, httpGroup["request_id"])
}

func TestRequestLoggerMiddleware_PropagatesProvidedHeader(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/echo", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/echo", nil)
	req.Header.Set("X-Request-ID", "trace-xyz")
	r.ServeHTTP(w, req)

	assert.Equal(t, "trace-xyz", w.Header().Get("X-Request-ID"))

	entry := readLastJSONLine(t, buf)
	assert.Equal(t, "trace-xyz", entry["trace_id"])
	httpGroup, ok := entry["http"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "trace-xyz", httpGroup["request_id"])
}

func TestRequestLoggerMiddleware_AttachesRequestContext(t *testing.T) {
	t.Parallel()
	log, _ := newJSONLogger()

	var captured ports.RequestContext
	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/rc", func(c *gin.Context) {
		captured = ports.FromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rc", nil)
	r.ServeHTTP(w, req)

	headerID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, headerID)
	assert.Equal(t, headerID, captured.TraceID)
	require.NotNil(t, captured.Logger)
}

func TestRequestLoggerMiddleware_LoggerFromContextReturnsDomained(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/inner", func(c *gin.Context) {
		ports.LoggerFromContext(c.Request.Context()).Info("handler-msg")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/inner", nil)
	r.ServeHTTP(w, req)

	headerID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, headerID)

	// Find the handler-msg entry (not the middleware's http_request entry).
	var handlerEntry map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var e map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &e), "raw: %s", line)
		if e["msg"] == "handler-msg" {
			handlerEntry = e
			break
		}
	}
	require.NotNil(t, handlerEntry, "expected handler-msg log line; buf=%s", buf.String())
	assert.Equal(t, "http", handlerEntry["domain"])
	assert.Equal(t, headerID, handlerEntry["trace_id"])
}

func TestRequestLoggerMiddleware_BridgesWithLoggerWithTraceID(t *testing.T) {
	t.Parallel()
	log, _ := newJSONLogger()

	var seen string
	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/bridge", func(c *gin.Context) {
		seen = logger.TraceID(c.Request.Context())
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/bridge", nil)
	r.ServeHTTP(w, req)

	headerID := w.Header().Get("X-Request-ID")
	require.NotEmpty(t, headerID)
	assert.Equal(t, headerID, seen, "internal/logger.TraceID must return the same trace_id (bridge invariant)")
}

func TestRequestLoggerMiddleware_ConcurrentRequestsHaveDistinctTraceIDs(t *testing.T) {
	t.Parallel()
	log, _ := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	const n = 32
	var (
		mu  sync.Mutex
		set = make(map[string]struct{}, n)
		wg  sync.WaitGroup
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ping", nil)
			r.ServeHTTP(w, req)
			id := w.Header().Get("X-Request-ID")
			mu.Lock()
			set[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	assert.Len(t, set, n, "expected %d unique trace_ids, got %d", n, len(set))
}

func TestRequestLoggerMiddleware_StatusCodePreserved(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/missing", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", nil)
	r.ServeHTTP(w, req)

	entry := readLastJSONLine(t, buf)
	httpGroup, ok := entry["http"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(http.StatusNotFound), httpGroup["status"])
}

func TestRequestLoggerMiddleware_DurationMsPositive(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/sleep", func(c *gin.Context) {
		time.Sleep(5 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/sleep", nil)
	r.ServeHTTP(w, req)

	entry := readLastJSONLine(t, buf)
	httpGroup, ok := entry["http"].(map[string]any)
	require.True(t, ok)
	durF, ok := httpGroup["duration_ms"].(float64)
	require.True(t, ok, "duration_ms missing or wrong type: %+v", httpGroup)
	assert.GreaterOrEqual(t, durF, 5.0, "duration must be at least the slept time")
}

func TestRequestLoggerMiddleware_LogRecordIsValidJSON_NoDomainNull(t *testing.T) {
	t.Parallel()
	log, buf := newJSONLogger()

	r := gin.New()
	r.Use(middleware.RequestLoggerMiddleware(log))
	r.GET("/check", func(c *gin.Context) {
		ports.LoggerFromContext(c.Request.Context()).Info("inner")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/check", nil)
	r.ServeHTTP(w, req)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2, "expected at least middleware + handler log lines")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry), "invalid JSON line: %s", line)
		domain, ok := entry["domain"].(string)
		require.True(t, ok, "domain key missing or non-string on line: %s", line)
		assert.NotEmpty(t, domain, "domain must not be empty on line: %s", line)
	}
}
