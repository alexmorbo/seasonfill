package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ginTestModeOnce sync.Once

func setupRequireAuthRouter(apiKey, cookieSecret string) *gin.Engine {
	ginTestModeOnce.Do(func() { gin.SetMode(gin.TestMode) })
	r := gin.New()
	api := r.Group("/api")
	api.Use(RequireAuth(apiKey, cookieSecret))
	api.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

// --- Header path -----------------------------------------------------------

func TestRequireAuth_HeaderPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		expectedKey string
		sentKey     string
		wantStatus  int
	}{
		{"valid key passes", "secret123", "secret123", http.StatusOK},
		{"wrong key rejected", "secret123", "wrong", http.StatusUnauthorized},
		{"missing key rejected", "secret123", "", http.StatusUnauthorized},
		{"empty expected always rejects header", "", "anything", http.StatusUnauthorized},
		{"empty expected with empty sent rejects too", "", "", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := setupRequireAuthRouter(tt.expectedKey, "cookie-secret")
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
			if tt.sentKey != "" {
				req.Header.Set("X-Api-Key", tt.sentKey)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// --- Cookie path -----------------------------------------------------------

func TestRequireAuth_ValidCookie_Allows(t *testing.T) {
	t.Parallel()
	secret := "cookie-secret"
	tok, err := SignCookie([]byte(secret), time.Now())
	require.NoError(t, err)

	r := setupRequireAuthRouter("api-key", secret)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAuth_BadCookie_FallsBackToHeader(t *testing.T) {
	t.Parallel()
	expiredTok, err := SignCookie([]byte("cookie-secret"), time.Now().Add(-60*24*time.Hour))
	require.NoError(t, err)
	mismatchTok, err := SignCookie([]byte("secretA"), time.Now())
	require.NoError(t, err)
	cases := []struct {
		name   string
		secret string
		value  string
	}{
		{"garbage", "cookie-secret", "garbage"},
		{"expired", "cookie-secret", expiredTok},
		{"secret_mismatch", "secretB", mismatchTok},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := setupRequireAuthRouter("api-key", tc.secret)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tc.value})
			req.Header.Set("X-Api-Key", "api-key")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code,
				"a bad cookie must not block a valid X-Api-Key header")
		})
	}
}

func TestRequireAuth_NoAuth_401(t *testing.T) {
	t.Parallel()
	r := setupRequireAuthRouter("api-key", "cookie-secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_TimingNotLeaked(t *testing.T) {
	t.Parallel()
	// All three failure modes return the same body — a probe can't
	// distinguish malformed vs expired vs wrong-header.
	expiredTok, _ := SignCookie([]byte("cookie-secret"),
		time.Now().Add(-60*24*time.Hour))
	cases := []func(req *http.Request){
		func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "garbage"})
		},
		func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: expiredTok})
			req.Header.Set("X-Api-Key", "wrong")
		},
		func(req *http.Request) { req.Header.Set("X-Api-Key", "wrong") },
	}
	var bodies []string
	for _, modify := range cases {
		r := setupRequireAuthRouter("api-key", "cookie-secret")
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
		modify(req)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		bodies = append(bodies, w.Body.String())
	}
	assert.Equal(t, bodies[0], bodies[1])
	assert.Equal(t, bodies[1], bodies[2])
}

// --- APIKeyAuth shim (webhook contract) -----------------------------------

func TestAPIKeyAuth_BehavesAsHeaderOnly(t *testing.T) {
	t.Parallel()
	ginTestModeOnce.Do(func() { gin.SetMode(gin.TestMode) })
	r := gin.New()
	api := r.Group("/api")
	api.Use(APIKeyAuth("webhook-secret"))
	api.POST("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	tok, err := SignCookie([]byte("anything"), time.Now())
	require.NoError(t, err)
	cases := []struct {
		name   string
		header string
		cookie string
		want   int
	}{
		{"valid header", "webhook-secret", "", http.StatusOK},
		// Cookie must NOT bypass webhook (privilege-isolation invariant):
		// the shim passes empty cookieSecret so VerifyCookie always fails.
		{"valid session cookie rejected", "", tok, http.StatusUnauthorized},
		{"wrong header", "wrong", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/x", nil)
			if tc.header != "" {
				req.Header.Set("X-Api-Key", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tc.cookie})
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
}
