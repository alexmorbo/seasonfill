package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

func slideHandler(t *testing.T, ttl time.Duration) (*gin.Engine, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	key := []byte("test-session-key-32-bytes-padded")
	ptr := &AuthRuntimePointer{}
	ptr.Store(&AuthRuntime{
		Mode: runtime.AuthModeForms, SessionTTL: ttl, SecureCookie: false,
	})
	r := gin.New()
	r.GET("/api/v1/scans",
		RequireAuthWithRuntime("api-key-test", key, ptr, nil, nil),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)
	return r, key
}

func mintCookie(t *testing.T, key []byte, exp time.Time) string {
	t.Helper()
	tok, err := SignSession(key, "alice", exp, 0)
	assert.NoError(t, err)
	return tok
}

func setCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == SessionCookieName {
			return c
		}
	}
	return nil
}

func TestSlidingCookie_AboveThreshold_NoReissue(t *testing.T) {
	t.Parallel()
	ttl := time.Hour
	r, key := slideHandler(t, ttl)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/scans", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: mintCookie(t, key, time.Now().Add(ttl-5*time.Minute))})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, setCookie(t, w))
}

func TestSlidingCookie_BelowThreshold_Reissues(t *testing.T) {
	t.Parallel()
	ttl := time.Hour
	r, key := slideHandler(t, ttl)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/scans", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: mintCookie(t, key, time.Now().Add(ttl/3))})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	got := setCookie(t, w)
	if got == nil {
		t.Fatal("expected Set-Cookie below threshold")
	}
	p, err := VerifySession(key, got.Value, time.Now(), 0)
	assert.NoError(t, err)
	assert.InDelta(t, ttl.Seconds(), time.Until(time.Unix(p.Exp, 0)).Seconds(), 5)
}

func TestSlidingCookie_AlreadyExpired_401(t *testing.T) {
	t.Parallel()
	r, key := slideHandler(t, time.Hour)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/scans", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: mintCookie(t, key, time.Now().Add(-time.Second))})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_REQUIRED")
}

func TestSlidingCookie_APIKey_NoReissue(t *testing.T) {
	t.Parallel()
	r, _ := slideHandler(t, time.Hour)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/scans", nil)
	req.Header.Set("X-Api-Key", "api-key-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, setCookie(t, w))
}
