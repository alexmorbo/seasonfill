package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

func setupAuth(t *testing.T, apiKey string) *gin.Engine {
	t.Helper()
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	r := gin.New()
	api := r.Group("/api")
	api.Use(RequireAuth(apiKey, sessionKey))
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	return r
}

func setupAuthWithRuntime(t *testing.T, apiKey string, rt *AuthRuntime) (*gin.Engine, *AuthRuntimePointer) {
	t.Helper()
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	ptr := &AuthRuntimePointer{}
	ptr.Store(rt)
	r := gin.New()
	api := r.Group("/api")
	api.Use(RequireAuthWithRuntime(apiKey, sessionKey, ptr, nil, nil))
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	return r, ptr
}

func TestRequireAuth_ValidAPIKey(t *testing.T) {
	t.Parallel()
	r := setupAuth(t, "secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("X-Api-Key", "secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAuth_ValidCookie(t *testing.T) {
	t.Parallel()
	sessionKey, err := crypto.DeriveSessionHMACKey("secret")
	require.NoError(t, err)
	tok, err := SignSession(sessionKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)
	r := setupAuth(t, "secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"user":"admin"`)
}

func TestRequireAuth_BothFail_401(t *testing.T) {
	t.Parallel()
	r := setupAuth(t, "secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "junk"})
	req.Header.Set("X-Api-Key", "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_NoAuth_401(t *testing.T) {
	t.Parallel()
	r := setupAuth(t, "secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_IdenticalRejection(t *testing.T) {
	t.Parallel()
	r := setupAuth(t, "secret")
	cases := []func(*http.Request){
		func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "junk"})
		},
		func(req *http.Request) { req.Header.Set("X-Api-Key", "wrong") },
		func(req *http.Request) {},
	}
	var bodies []string
	for _, mut := range cases {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
		mut(req)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		bodies = append(bodies, w.Body.String())
	}
	assert.Equal(t, bodies[0], bodies[1])
	assert.Equal(t, bodies[1], bodies[2])
}

// TestRequireAuth_DispatchMatrix exercises the mode × auth-state matrix
// that 036a wires up. Each row asserts the expected outcome given a
// snapshot mode and combination of present/absent cookie/api-key.
func TestRequireAuth_DispatchMatrix(t *testing.T) {
	t.Parallel()
	const apiKey = "secret"
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	validCookie, err := SignSession(sessionKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)

	type want struct {
		status int
		user   string
	}
	cases := []struct {
		name   string
		mode   string
		apiKey string
		cookie string
		want   want
	}{
		{"forms+valid_cookie", runtime.AuthModeForms, "", validCookie, want{http.StatusOK, "admin"}},
		{"forms+valid_apikey", runtime.AuthModeForms, apiKey, "", want{http.StatusOK, "api-key"}},
		{"forms+no_auth", runtime.AuthModeForms, "", "", want{http.StatusUnauthorized, ""}},
		{"forms+wrong_apikey", runtime.AuthModeForms, "nope", "", want{http.StatusUnauthorized, ""}},
		{"basic+no_header_falls_through", runtime.AuthModeBasic, "", "", want{http.StatusUnauthorized, ""}},
		{"basic+apikey_works", runtime.AuthModeBasic, apiKey, "", want{http.StatusOK, "api-key"}},
		{"basic+cookie_ignored_no_repo", runtime.AuthModeBasic, "", validCookie, want{http.StatusUnauthorized, ""}},
		{"none+no_auth_passes", runtime.AuthModeNone, "", "", want{http.StatusOK, "anonymous"}},
		{"none+apikey_identity", runtime.AuthModeNone, apiKey, "", want{http.StatusOK, "api-key"}},
		{"none+cookie_irrelevant", runtime.AuthModeNone, "", validCookie, want{http.StatusOK, "anonymous"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, _ := setupAuthWithRuntime(t, apiKey, &AuthRuntime{Mode: tc.mode})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
			if tc.apiKey != "" {
				req.Header.Set("X-Api-Key", tc.apiKey)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tc.want.status, w.Code, "body=%s", w.Body.String())
			if tc.want.user != "" {
				assert.Contains(t, w.Body.String(), `"user":"`+tc.want.user+`"`)
			}
		})
	}
}

// TestRequireAuth_StaleEpochCookie_Rejected confirms a cookie minted
// under an older epoch is rejected after a mode change bumps the
// authoritative epoch.
func TestRequireAuth_StaleEpochCookie_Rejected(t *testing.T) {
	t.Parallel()
	const apiKey = "secret"
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	oldCookie, err := SignSession(sessionKey, "admin", time.Now().Add(time.Hour), 100)
	require.NoError(t, err)

	r, _ := setupAuthWithRuntime(t, apiKey, &AuthRuntime{
		Mode:         runtime.AuthModeForms,
		SessionEpoch: 200,
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: oldCookie})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
