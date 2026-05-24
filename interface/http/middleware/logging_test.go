package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/logger"
)

func TestRequestLogging_GeneratesRequestID_WhenAbsent(t *testing.T) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := gin.New()
	r.Use(RequestLogging(log))
	r.GET("/ping", func(c *gin.Context) {
		// Confirm a trace_id is propagated into the request context.
		assert.NotEmpty(t, logger.TraceID(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ping", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	assert.Equal(t, "http_request", entry["msg"])

	httpGroup, ok := entry["http"].(map[string]any)
	require.True(t, ok, "expected http group in log entry")
	assert.Equal(t, "GET", httpGroup["method"])
	assert.Equal(t, "/ping", httpGroup["path"])
	assert.Equal(t, float64(http.StatusOK), httpGroup["status"])
}

func TestRequestLogging_PropagatesProvidedRequestID(t *testing.T) {
	buf := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(buf, nil))

	r := gin.New()
	r.Use(RequestLogging(log))
	r.GET("/echo", func(c *gin.Context) {
		assert.Equal(t, "trace-xyz", logger.TraceID(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/echo", nil)
	req.Header.Set("X-Request-ID", "trace-xyz")
	r.ServeHTTP(w, req)

	assert.Equal(t, "trace-xyz", w.Header().Get("X-Request-ID"))
	assert.True(t, strings.Contains(buf.String(), `"request_id":"trace-xyz"`),
		"trace id from request header must appear in log output, got %s", buf.String())
}
