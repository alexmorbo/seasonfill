package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
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

func TestAuthConfig_DefaultShape(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.OIDCReady)
}

func TestAuthConfig_NilRuntime(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, nil)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.OIDCReady)
}

// TestAuthConfig_NoSecretsLeaked confirms the wire shape carries ONLY
// {oidc_ready}. Adding fields here is intentional churn — keep the
// assertion list explicit.
func TestAuthConfig_NoSecretsLeaked(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		SessionEpoch:   9999,                 // must NOT appear in the response
		TrustedProxies: []string{"10.0.0.1"}, // must NOT appear either
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, len(body), "auth_config wire shape must be exactly {oidc_ready}")
	assert.Contains(t, body, "oidc_ready")
	assert.NotContains(t, body, "mode")
	assert.Equal(t, false, body["oidc_ready"])
}

// TestAuthConfig_ReturnsModeOIDCWithLoginURL confirms that fully populated
// OIDC fields add login_url and oidc_ready=true.
func TestAuthConfig_ReturnsModeOIDCWithLoginURL(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		OIDC: middleware.OIDCRuntime{
			Issuer:       "https://sso.example.com",
			ClientID:     "sf",
			ClientSecret: "secret",
		},
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.OIDCReady)
	assert.Equal(t, "/api/v1/auth/oidc/start", body.LoginURL)
}

// TestAuthConfig_OIDCReadyEmitsLoginURL — OIDC fully populated →
// oidc_ready=true and login_url set.
func TestAuthConfig_OIDCReadyEmitsLoginURL(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		OIDC: middleware.OIDCRuntime{
			Issuer:       "https://sso.example.com",
			ClientID:     "sf",
			ClientSecret: "secret",
		},
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.OIDCReady)
	assert.Equal(t, "/api/v1/auth/oidc/start", body.LoginURL)
}

// TestAuthConfig_OIDCReadyFalse_NoLoginURL — OIDC missing ClientSecret →
// oidc_ready=false, no login_url key.
func TestAuthConfig_OIDCReadyFalse_NoLoginURL(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{
		OIDC: middleware.OIDCRuntime{
			Issuer:   "https://sso.example.com",
			ClientID: "sf",
			// ClientSecret intentionally empty
		},
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.Equal(t, false, raw["oidc_ready"])
	assert.NotContains(t, raw, "login_url", "login_url must be absent when oidc_ready=false")
}
