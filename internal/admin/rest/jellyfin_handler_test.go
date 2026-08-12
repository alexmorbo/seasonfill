package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// setupJellyfin wires a gin engine with the real JellyfinLoginUseCase +
// handler over a fake repo. jellyfinURL is stamped into AuthRuntime.Jellyfin
// (empty => not-configured path).
func setupJellyfin(t *testing.T, repo *fakeAdminRepo, jellyfinURL string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{
		SessionTTL: time.Hour,
		Jellyfin:   middleware.JellyfinRuntime{BaseURL: jellyfinURL, Enabled: jellyfinURL != ""},
	})
	uc := auth.NewJellyfinLoginUseCase(repo)
	h := NewJellyfinHandler(uc, ptr, []byte("test-key"), time.Hour, false, nil)
	r := gin.New()
	r.POST("/api/v1/auth/jellyfin/login", h.Login)
	return r
}

// newJellyfinTestServer stands up a fake Jellyfin AuthenticateByName endpoint.
func newJellyfinTestServer(t *testing.T, valid bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !valid {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"User":{"Id":"jf-42","Name":"alice"},"AccessToken":"tok"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJellyfinHandler_Valid(t *testing.T) {
	jf := newJellyfinTestServer(t, true)
	repo := &fakeAdminRepo{}
	r := setupJellyfin(t, repo, jf.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/auth/jellyfin/login",
		strings.NewReader(`{"username":"alice","password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok":true`)
	assert.Contains(t, w.Body.String(), `"username":"alice"`)
	cookie := w.Header().Get("Set-Cookie")
	assert.Contains(t, cookie, middleware.SessionCookieName+"=")
	assert.Contains(t, cookie, "HttpOnly")
}

func TestJellyfinHandler_InvalidCreds(t *testing.T) {
	jf := newJellyfinTestServer(t, false)
	repo := &fakeAdminRepo{}
	r := setupJellyfin(t, repo, jf.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/auth/jellyfin/login",
		strings.NewReader(`{"username":"alice","password":"wrong"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "UNAUTHORIZED")
}

func TestJellyfinHandler_NotConfigured(t *testing.T) {
	repo := &fakeAdminRepo{}
	r := setupJellyfin(t, repo, "")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/auth/jellyfin/login",
		strings.NewReader(`{"username":"alice","password":"pw"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "JELLYFIN_NOT_CONFIGURED")
}

func TestJellyfinHandler_BadContentType(t *testing.T) {
	jf := newJellyfinTestServer(t, true)
	repo := &fakeAdminRepo{}
	r := setupJellyfin(t, repo, jf.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/auth/jellyfin/login",
		strings.NewReader(`username=alice`))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestJellyfinHandler_EmptyBody(t *testing.T) {
	jf := newJellyfinTestServer(t, true)
	repo := &fakeAdminRepo{}
	r := setupJellyfin(t, repo, jf.URL)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/auth/jellyfin/login",
		strings.NewReader(`{"username":"","password":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}
