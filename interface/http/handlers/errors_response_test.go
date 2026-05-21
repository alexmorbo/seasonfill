package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errDriver is the synthetic raw error used to verify that nothing
// driver-specific leaks across the HTTP boundary.
var errDriver = errors.New(`driver: pq: relation "foo" does not exist`)

func runHelper(t *testing.T, logger *slog.Logger, attrs ...slog.Attr) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/boom", func(c *gin.Context) {
		writeInternalError(c, logger, "audit_list_scans_failed", errDriver, attrs...)
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestWriteInternalError_GenericResponse: client must NEVER see the
// driver text — only the generic message.
func TestWriteInternalError_GenericResponse(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	w := runHelper(t, logger)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "internal server error", body["error"])
	assert.NotContains(t, w.Body.String(), "pq:")
	assert.NotContains(t, w.Body.String(), "relation")
}

// TestWriteInternalError_LogsRealError: operator log MUST carry the
// driver text + the caller's contextual attrs.
func TestWriteInternalError_LogsRealError(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_ = runHelper(t, logger,
		slog.String("endpoint", "/api/v1/scans"),
		slog.String("instance", "main"),
	)

	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(logBuf.String())), &rec))
	assert.Equal(t, "audit_list_scans_failed", rec["msg"])
	assert.Equal(t, "ERROR", rec["level"])
	assert.Contains(t, rec["error"], "pq:")
	assert.Equal(t, "/api/v1/scans", rec["endpoint"])
	assert.Equal(t, "main", rec["instance"])
}

// TestWriteInternalError_NilLoggerFallback: nil logger MUST NOT panic.
func TestWriteInternalError_NilLoggerFallback(t *testing.T) {
	assert.NotPanics(t, func() {
		w := runHelper(t, nil)
		require.Equal(t, http.StatusInternalServerError, w.Code)
	})
}
