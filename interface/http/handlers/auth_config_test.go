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

// TestAuthConfig_NoSecretsLeaked confirms the wire shape for non-oidc
// modes carries ONLY mode + local_bypass. Adding fields here is
// intentional churn — keep the assertion list explicit.
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
	assert.Equal(t, 2, len(body), "non-oidc auth_config wire shape must be exactly {mode, local_bypass}")
	assert.Contains(t, body, "mode")
	assert.Contains(t, body, "local_bypass")
}

// TestAuthConfig_ReturnsModeOIDCWithLoginURL confirms that mode=oidc adds
// the login_url field to the wire response (SPA uses it to render the
// "Login with SSO" button), while other fields stay private.
func TestAuthConfig_ReturnsModeOIDCWithLoginURL(t *testing.T) {
	t.Parallel()
	r := setupAuthConfig(t, &middleware.AuthRuntime{Mode: runtime.AuthModeOIDC})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body dto.AuthConfigDTO
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "oidc", body.Mode)
	assert.Equal(t, "/api/v1/auth/oidc/start", body.LoginURL)
}

// TestAuthConfig_NonOIDC_NoLoginURL confirms login_url is absent (omitempty)
// for non-oidc modes so the SPA never renders a stale SSO button.
func TestAuthConfig_NonOIDC_NoLoginURL(t *testing.T) {
	t.Parallel()
	cases := []string{
		runtime.AuthModeForms,
		runtime.AuthModeBasic,
		runtime.AuthModeNone,
	}
	for _, mode := range cases {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := setupAuthConfig(t, &middleware.AuthRuntime{Mode: mode})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/config", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
			assert.NotContains(t, raw, "login_url",
				"mode=%s must not expose login_url", mode)
		})
	}
}
