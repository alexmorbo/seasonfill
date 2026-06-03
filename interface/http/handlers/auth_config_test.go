package handlers

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func setupAuthConfig(t *testing.T, rt *middleware.AuthRuntime) *gin.Engine {
	t.Helper()
	ptr := &middleware.AuthRuntimePointer{}
	if rt != nil {
		ptr.Store(rt)
	}
	h := NewAuthConfigHandler(ptr)
	r := gin.New()
	r.GET("/api/v1/auth/config", h.Get)
	return r
}

func TestAuthConfig_ReturnsModeForms(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{Mode: runtime.AuthModeForms, LocalBypass: false})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "forms", body.Mode)
	assert.False(t, body.LocalBypass)
}

func TestAuthConfig_ReturnsModeBasicWithBypass(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		Mode:        runtime.AuthModeBasic,
		LocalBypass: true,
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "basic", body.Mode)
	assert.True(t, body.LocalBypass)
}

func TestAuthConfig_ReturnsModeNone(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{Mode: runtime.AuthModeNone})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "none", body.Mode)
}

func TestAuthConfig_FallsBackToFormsOnNilRuntime(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "forms", body.Mode)
}

// TestAuthConfig_NoSecretsLeaked confirms the wire shape carries ONLY
// mode + local_bypass. Adding fields here is intentional churn — keep
// the assertion list explicit.
func TestAuthConfig_NoSecretsLeaked(t *testing.T) {
	t.Parallel()
	_, ipnet, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		Mode:           runtime.AuthModeForms,
		SessionEpoch:   9999,                 // must NOT appear in the response
		LocalNetworks:  []*net.IPNet{ipnet},  // must NOT appear in the response
		TrustedProxies: []string{"10.0.0.1"}, // must NOT appear either
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 2, len(body), "auth_config wire shape must be exactly {mode, local_bypass}")
	assert.Contains(t, body, "mode")
	assert.Contains(t, body, "local_bypass")
}
