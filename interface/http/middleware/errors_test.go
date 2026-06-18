package middleware_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(discardLogger()))
	r.GET("/x", handler)
	return r
}

func TestErrorResponseMiddleware_TypedNotFound(t *testing.T) {
	t.Parallel()

	id := int64(99)
	r := newRouter(func(c *gin.Context) {
		_ = c.Error(&sharedErrors.SeriesNotFoundError{ID: id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body errorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "series_not_found", body.Error)
	assert.Equal(t, fmt.Sprintf("series %d not found", id), body.Message)
}

func TestErrorResponseMiddleware_WrappedRetriable(t *testing.T) {
	t.Parallel()

	inner := &sharedErrors.SonarrUnreachableError{
		Instance: "main",
		Cause:    errors.New("dial tcp: connection refused"),
	}
	wrapped := fmt.Errorf("call sonarr: %w", inner)

	r := newRouter(func(c *gin.Context) {
		_ = c.Error(wrapped)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)

	var body errorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "sonarr_unreachable", body.Error)
	assert.Contains(t, body.Message, "sonarr instance \"main\" unreachable")
}

func TestErrorResponseMiddleware_NoErrorIsNoop(t *testing.T) {
	t.Parallel()

	r := newRouter(func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["ok"])
}

func TestErrorResponseMiddleware_HandlerAlreadyWrote(t *testing.T) {
	t.Parallel()

	// Handler writes 202 then pushes a typed error. Middleware MUST
	// respect c.Writer.Written() and leave the original response intact.
	r := newRouter(func(c *gin.Context) {
		c.JSON(http.StatusAccepted, gin.H{"queued": true})
		_ = c.Error(&sharedErrors.ScanFailedError{Cause: errors.New("disk full")})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code,
		"middleware must not overwrite handler's response")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, true, body["queued"])
	_, hasErrorKey := body["error"]
	assert.False(t, hasErrorKey, "untouched body must not gain an error key")
}

func TestErrorResponseMiddleware_UntypedError(t *testing.T) {
	t.Parallel()

	r := newRouter(func(c *gin.Context) {
		_ = c.Error(errors.New("boom"))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var body errorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "internal_error", body.Error)
	assert.Equal(t, "boom", body.Message)
}

func TestErrorResponseMiddleware_UsesLastErrorWhenMultiplePushed(t *testing.T) {
	t.Parallel()

	// Sanity: if a handler pushes multiple errors, middleware picks
	// c.Errors.Last() per the documented contract.
	r := newRouter(func(c *gin.Context) {
		_ = c.Error(errors.New("first"))
		_ = c.Error(&sharedErrors.ScanInProgressError{})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)

	var body errorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "scan_in_progress", body.Error)
}
