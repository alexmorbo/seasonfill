package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
)

const (
	testAPIKey       = "admin-key-please-rotate"
	testCookieSecret = "test-cookie-secret-32-bytes-long"
)

var ginTestModeOnce sync.Once

// newAuthRouter wires both endpoints WITHOUT a guard on /session;
// 009a1's server.go adds the guard. Handler-only tests live here.
func newAuthRouter(t *testing.T) *gin.Engine {
	t.Helper()
	ginTestModeOnce.Do(func() { gin.SetMode(gin.TestMode) })
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewAuthHandler(testAPIKey, testCookieSecret, false, lg)

	r := gin.New()
	auth := r.Group("/api/v1/auth")
	auth.POST("/login", h.Login)
	auth.DELETE("/session", h.Logout)
	return r
}

func findSessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == middleware.SessionCookieName {
			return c
		}
	}
	return nil
}

// --- Login: body path ------------------------------------------------------

func TestAuthHandler_Login_ValidKey_SetsCookie(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	body := []byte(`{"api_key":"` + testAPIKey + `"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	cookie := findSessionCookie(t, w)
	require.NotNil(t, cookie, "expected Set-Cookie %s", middleware.SessionCookieName)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.Equal(t, cookieMaxAgeSeconds, cookie.MaxAge)
	// Token must verify with the same secret.
	assert.NoError(t, middleware.VerifyCookie([]byte(testCookieSecret),
		cookie.Value, time.Now()))
}

// --- Login: header fallback -----------------------------------------------

func TestAuthHandler_Login_ValidKey_HeaderFallback(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", nil)
	req.Header.Set("X-Api-Key", testAPIKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, findSessionCookie(t, w))
}

func TestAuthHandler_Login_BodyWinsOverHeader(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	// Header is wrong, body is right → success (body wins).
	body := []byte(`{"api_key":"` + testAPIKey + `"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "this-is-wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// --- Login: failure paths -------------------------------------------------

func TestAuthHandler_Login_InvalidKey_401(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	body := []byte(`{"api_key":"wrong"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), `"error":"unauthorized"`)
	assert.Contains(t, w.Body.String(), `"code":"UNAUTHORIZED"`)
	assert.Nil(t, findSessionCookie(t, w), "must not set cookie on bad key")
}

func TestAuthHandler_Login_NoKey_401(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Nil(t, findSessionCookie(t, w))
}

func TestAuthHandler_Login_EmptyBodyKeyFallsBackToHeader_NoHeader_401(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	body := []byte(`{"api_key":""}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthHandler_Login_MalformedBody_400(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader([]byte(`{"api_key":`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "malformed body")
}

func TestAuthHandler_Login_OversizedBody_Rejected(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	// 10 KiB body — well above the 4 KiB cap.
	big := `{"api_key":"` + strings.Repeat("A", 10<<10) + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader([]byte(big)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Cap triggers MaxBytesError → respondInvalidKey → 401.
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Login: Content-Type gating (H1) --------------------------------------

func TestAuthHandler_Login_FormContentTypeIgnoresBody(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	// CORS-simple form-POST probe: real key in body must be ignored.
	body := []byte("api_key=" + testAPIKey)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Nil(t, findSessionCookie(t, w))
}

func TestAuthHandler_Login_FormContentTypeWithHeader_Succeeds(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	// Form Content-Type → body ignored, header path authenticates.
	body := []byte("api_key=ignored")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Api-Key", testAPIKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, findSessionCookie(t, w))
}

// --- Login: clock injection (M1) ------------------------------------------

func TestAuthHandler_Login_ClockInjection(t *testing.T) {
	t.Parallel()
	ginTestModeOnce.Do(func() { gin.SetMode(gin.TestMode) })
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))

	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	h := NewAuthHandler(testAPIKey, testCookieSecret, false, lg,
		WithClock(func() time.Time { return fixed }))

	r := gin.New()
	r.POST("/api/v1/auth/login", h.Login)

	body := []byte(`{"api_key":"` + testAPIKey + `"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	cookie := findSessionCookie(t, w)
	require.NotNil(t, cookie)
	// Cookie verifies at the injected instant — proves SignCookie
	// was called with h.now() and not real time.Now.
	require.NoError(t, middleware.VerifyCookie(
		[]byte(testCookieSecret), cookie.Value, fixed))
	require.NoError(t, middleware.VerifyCookie(
		[]byte(testCookieSecret), cookie.Value,
		fixed.Add(29*24*time.Hour)))
}

// --- Constructor (H2) ------------------------------------------------------

func TestNewAuthHandler_PanicsOnEmptyKey(t *testing.T) {
	t.Parallel()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	assert.PanicsWithValue(t,
		"handlers.NewAuthHandler: apiKey must not be empty",
		func() {
			_ = NewAuthHandler("", testCookieSecret, false, lg)
		},
		"empty apiKey is a server-misconfig and must fail fast")
}

// --- Logout (handler-only; guard added in 009a1) --------------------------

func TestAuthHandler_Logout_ClearsCookie(t *testing.T) {
	t.Parallel()
	r := newAuthRouter(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/api/v1/auth/session", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	cookie := findSessionCookie(t, w)
	require.NotNil(t, cookie, "expected clearing Set-Cookie")
	assert.Empty(t, cookie.Value)
	assert.LessOrEqual(t, cookie.MaxAge, 0, "Max-Age must clear the cookie")
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, "/", cookie.Path)
}
