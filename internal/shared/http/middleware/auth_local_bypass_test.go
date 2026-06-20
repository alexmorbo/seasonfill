package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// localNets returns the parsed RFC1918 + loopback CIDRs used by the
// bypass tests. Kept small (no IPv6) so test setup stays focused.
func localNets(t *testing.T) []*net.IPNet {
	t.Helper()
	_, n1, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)
	_, n2, err := net.ParseCIDR("127.0.0.0/8")
	require.NoError(t, err)
	_, n3, err := net.ParseCIDR("192.168.0.0/16")
	require.NoError(t, err)
	return []*net.IPNet{n1, n2, n3}
}

// setupBypass wires a gin engine with RequireAuthWithRuntime on /api/*
// and RequireAuthWebhook on /api/webhook/*. trustedProxies=[] so
// c.ClientIP() reflects RemoteAddr only — no XFF spoofing path.
func setupBypass(t *testing.T, apiKey string, rt *AuthRuntime) *gin.Engine {
	t.Helper()
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	ptr := &AuthRuntimePointer{}
	ptr.Store(rt)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	api := r.Group("/api")
	api.Use(RequireAuthWithRuntime(apiKey, sessionKey, ptr, nil, nil))
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	wh := r.Group("/api/webhook/sonarr/:instance_name")
	wh.Use(RequireAuthWebhook(apiKey, sessionKey, ptr, nil, nil))
	wh.POST("", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	return r
}

func reqWithRemote(t *testing.T, method, path, remoteAddr string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.RemoteAddr = remoteAddr
	return req
}

func TestLocalBypass_FormsMode_LocalIP_AllowsAnonymous(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "k", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodGet, "/api/ping", "10.5.6.7:12345"))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"local"`)
}

func TestLocalBypass_BasicMode_LocalIP_AllowsAnonymous(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "k", &AuthRuntime{
		Mode: runtime.AuthModeBasic, LocalBypass: true, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodGet, "/api/ping", "192.168.1.5:9999"))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"local"`)
}

func TestLocalBypass_NoneMode_LocalIP_AllowsAnonymous(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "k", &AuthRuntime{
		Mode: runtime.AuthModeNone, LocalBypass: true, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodGet, "/api/ping", "10.0.0.1:8080"))
	require.Equal(t, http.StatusOK, w.Code)
	// mode=none would have allowed anyway; bypass step short-circuits
	// first → identity "local", not "anonymous".
	assert.Contains(t, w.Body.String(), `"user":"local"`)
}

func TestLocalBypass_FormsMode_PublicIP_Rejects(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "k", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodGet, "/api/ping", "8.8.8.8:443"))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestLocalBypass_Disabled_LocalIP_Rejects(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "k", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: false, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodGet, "/api/ping", "10.5.6.7:12345"))
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"bypass=false must NOT short-circuit even on a local IP")
}

func TestLocalBypass_APIKeyPrecedence_LocalIP(t *testing.T) {
	t.Parallel()
	// X-Api-Key MUST win over local-bypass — identity = "api-key", NOT "local".
	r := setupBypass(t, "topsecret", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	req := reqWithRemote(t, http.MethodGet, "/api/ping", "10.5.6.7:12345")
	req.Header.Set("X-Api-Key", "topsecret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"api-key"`,
		"valid X-Api-Key must attribute to api-key, never collapse to local")
}

func TestLocalBypass_Webhook_LocalIP_NoKey_Rejects(t *testing.T) {
	t.Parallel()
	// INVARIANT: webhook NEVER bypassed. Local IP + bypass=true + no key → 401.
	r := setupBypass(t, "topsecret", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, reqWithRemote(t, http.MethodPost,
		"/api/webhook/sonarr/alpha", "10.5.6.7:12345"))
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"webhook MUST require X-Api-Key even from a local IP")
}

func TestLocalBypass_Webhook_LocalIP_WithKey_Allows(t *testing.T) {
	t.Parallel()
	r := setupBypass(t, "topsecret", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	req := reqWithRemote(t, http.MethodPost, "/api/webhook/sonarr/alpha", "10.5.6.7:12345")
	req.Header.Set("X-Api-Key", "topsecret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"api-key"`)
}

func TestLocalBypass_Webhook_PublicIP_WithKey_Allows(t *testing.T) {
	t.Parallel()
	// Sanity check: webhook from a public IP with valid key is the
	// production happy path — must not regress.
	r := setupBypass(t, "topsecret", &AuthRuntime{
		Mode: runtime.AuthModeForms, LocalBypass: true, LocalNetworks: localNets(t),
	})
	req := reqWithRemote(t, http.MethodPost, "/api/webhook/sonarr/alpha", "203.0.113.5:443")
	req.Header.Set("X-Api-Key", "topsecret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
