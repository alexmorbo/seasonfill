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
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

func mustParseUUID(s string) uuid.UUID {
	u, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

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

	id := domain.SeriesID(99)
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

// TestErrorResponseMiddleware_PortsErrNotFoundFallback documents the
// F-2c-1 safety net: when a handler dispatches a bare ports.ErrNotFound
// (e.g. an application-layer use case that has not migrated to typed
// errors yet) the middleware downgrades to 404 with a generic
// `not_found` slug rather than the default 500 / `internal_error`.
// Typed errors always win — verified by the TypedNotFound test above.
func TestErrorResponseMiddleware_PortsErrNotFoundFallback(t *testing.T) {
	t.Parallel()

	r := newRouter(func(c *gin.Context) {
		_ = c.Error(ports.ErrNotFound)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body errorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "not_found", body.Error)
	assert.Equal(t, "not found", body.Message)
}

// TestErrorResponseMiddleware_HandlerDispatchIntegration mirrors the
// production wire flow: a representative handler returns a typed
// repository error via c.Error, and the middleware emits the snake_case
// slug on the `error` key with the typed Error() string on `message`.
func TestErrorResponseMiddleware_HandlerDispatchIntegration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantSlug   string
		wantPrefix string
	}{
		{
			name: "InstanceNotFoundError",
			err: errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: "alpha"},
				ports.ErrNotFound,
			),
			wantStatus: http.StatusNotFound,
			wantSlug:   "instance_not_found",
			wantPrefix: "instance \"alpha\" not found",
		},
		{
			name: "DecisionNotFoundError",
			err: errors.Join(
				&sharedErrors.DecisionNotFoundError{ID: testDecisionID},
				ports.ErrNotFound,
			),
			wantStatus: http.StatusNotFound,
			wantSlug:   "decision_not_found",
			wantPrefix: "decision",
		},
		{
			name: "GrabNotFoundError",
			err: errors.Join(
				&sharedErrors.GrabNotFoundError{ID: "gr-1"},
				ports.ErrNotFound,
			),
			wantStatus: http.StatusNotFound,
			wantSlug:   "grab_not_found",
			wantPrefix: "grab",
		},
		{
			name:       "ScanInProgressError",
			err:        &sharedErrors.ScanInProgressError{},
			wantStatus: http.StatusConflict,
			wantSlug:   "scan_in_progress",
			wantPrefix: "scan already in progress",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRouter(func(c *gin.Context) {
				_ = c.Error(tc.err)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", nil)
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())

			var body errorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, tc.wantSlug, body.Error, "slug on `error` key")
			assert.Contains(t, body.Message, tc.wantPrefix, "message contains typed err text")
		})
	}
}

// testDecisionID is a stable UUID used across the handler dispatch
// integration cases for predictable Error() text in assertions.
var testDecisionID = mustParseUUID("11111111-1111-1111-1111-111111111111")
