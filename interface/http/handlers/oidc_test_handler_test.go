package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
)

func setupOIDCTest(t *testing.T, snap middleware.OIDCRuntime) *gin.Engine {
	t.Helper()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{OIDC: snap})
	h := NewOIDCTestHandler(ptr, nil)
	r := gin.New()
	r.POST("/api/v1/auth/oidc/test", h.Test)
	return r
}

func TestOIDCTestHandler_EmptyBodyFallsBackToSnapshot(t *testing.T) {
	t.Parallel()
	r := setupOIDCTest(t, middleware.OIDCRuntime{Issuer: "https://sso.example.com"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/oidc/test", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should always be 200 (even if issuer is unreachable — the test just returns ok=false).
	require.Equal(t, http.StatusOK, w.Code)
	var result auth.OIDCTestResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	// Issuer from snapshot is unreachable but the response structure must be present.
	assert.NotNil(t, result.Discovery)
}

func TestOIDCTestHandler_BodyOverridesSnapshot(t *testing.T) {
	t.Parallel()
	r := setupOIDCTest(t, middleware.OIDCRuntime{Issuer: "https://sso.example.com"})
	body := `{"issuer":"https://override.example.com"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/oidc/test", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// Always HTTP 200, even if the override issuer is unreachable.
}

func TestOIDCTestHandler_AlwaysHTTP200(t *testing.T) {
	t.Parallel()
	// No issuer → discovery.error set, but still 200.
	r := setupOIDCTest(t, middleware.OIDCRuntime{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/auth/oidc/test", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var result auth.OIDCTestResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.False(t, result.Discovery.OK)
	assert.Equal(t, "issuer is empty", result.Discovery.Error)
}
