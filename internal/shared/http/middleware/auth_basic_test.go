package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/alexmorbo/seasonfill/application/ports"
	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

type fakeBasicAdminRepo struct {
	mu   sync.Mutex
	user *admin.AdminUser
	err  error
}

func (r *fakeBasicAdminRepo) Get(_ context.Context) (admin.AdminUser, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return admin.AdminUser{}, r.err
	}
	if r.user == nil {
		return admin.AdminUser{}, ports.ErrNotFound
	}
	return *r.user, nil
}
func (r *fakeBasicAdminRepo) Create(_ context.Context, _ admin.AdminUser) error {
	return nil
}
func (r *fakeBasicAdminRepo) UpdatePassword(_ context.Context, _ string, _ bool) error {
	return nil
}
func (r *fakeBasicAdminRepo) GetByOIDCSubject(_ context.Context, _ string) (admin.AdminUser, error) {
	return admin.AdminUser{}, ports.ErrNotFound
}
func (r *fakeBasicAdminRepo) CreateFromOIDC(_ context.Context, subject, username string) (admin.AdminUser, error) {
	sub := subject
	return admin.AdminUser{Username: username, OIDCSubject: &sub}, nil
}

func setupAuthBasic(
	t *testing.T,
	apiKey string,
	repo ports.AdminUserRepository,
	lim *auth.IPLimiter,
	rt *AuthRuntime,
) *gin.Engine {
	t.Helper()
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	ptr := &AuthRuntimePointer{}
	ptr.Store(rt)
	r := gin.New()
	api := r.Group("/api")
	api.Use(RequireAuthWithRuntime(apiKey, sessionKey, ptr, repo, lim))
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	return r
}

func seedBasicRepo(t *testing.T, username, password string) *fakeBasicAdminRepo {
	t.Helper()
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)
	return &fakeBasicAdminRepo{user: &admin.AdminUser{
		Username: username, PasswordHash: hash,
	}}
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestBasicAuth_HappyPath(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "hunter22"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"admin"`)
}

func TestBasicAuth_MissingHeader_401WithChallenge(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, `Basic realm="Seasonfill"`, w.Header().Get("WWW-Authenticate"))
	assert.Contains(t, w.Body.String(), `"code":"AUTH_REQUIRED"`)
}

func TestBasicAuth_MalformedHeader_401WithChallenge(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	cases := []struct {
		name   string
		header string
	}{
		{"bad_base64", "Basic !!!"},
		{"wrong_scheme", "Bearer " + base64.StdEncoding.EncodeToString([]byte("admin:hunter22"))},
		{"no_colon_in_payload", "Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon"))},
		{"empty_username", "Basic " + base64.StdEncoding.EncodeToString([]byte(":pw"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
			req.Header.Set("Authorization", tc.header)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Equal(t, `Basic realm="Seasonfill"`, w.Header().Get("WWW-Authenticate"))
		})
	}
}

func TestBasicAuth_WrongUsername_401NoChallenge(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("notadmin", "hunter22"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"),
		"WWW-Authenticate must NOT be set on bad-creds — prevents browser popup loop")
}

func TestBasicAuth_WrongPassword_401NoChallenge(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "wrong"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"))
}

func TestBasicAuth_NoAdminRow_401NoChallenge(t *testing.T) {
	t.Parallel()
	repo := &fakeBasicAdminRepo{} // no user seeded → ErrNotFound
	r := setupAuthBasic(t, "k", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "hunter22"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// constant-latency verify burns bcrypt then returns false → bad creds
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"))
}

func TestBasicAuth_APIKeyStillWorksInBasicMode(t *testing.T) {
	t.Parallel()
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "apikey-xyz", repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("X-Api-Key", "apikey-xyz")
	// no Authorization header — X-Api-Key path must short-circuit
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"user":"api-key"`)
}

// TestBasicAuth_CookieAcceptedInBasicMode — 037e: mode=basic now accepts a
// valid session cookie (e.g. issued after OIDC login) before falling through
// to the Basic challenge. Ensures the browser popup is suppressed for users
// with an active cookie.
func TestBasicAuth_CookieAcceptedInBasicMode(t *testing.T) {
	t.Parallel()
	const apiKey = "k"
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	cookie, err := SignSession(sessionKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, apiKey, repo, nil, &AuthRuntime{Mode: runtime.AuthModeBasic})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookie})
	// no Authorization header — valid cookie must auth us in Basic mode (037e)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("WWW-Authenticate"),
		"must not emit Basic challenge when cookie authenticates")
}

func TestBasicAuth_RateLimitShared(t *testing.T) {
	t.Parallel()
	// burst=2 so the 3rd bad-creds attempt is throttled to 429
	lim := auth.NewIPLimiter(rate.Every(time.Hour), 2)
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, lim, &AuthRuntime{Mode: runtime.AuthModeBasic})

	hit := func() int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
		req.Header.Set("Authorization", basicHeader("admin", "wrong"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusUnauthorized, hit(), "attempt 1: bad creds")
	assert.Equal(t, http.StatusUnauthorized, hit(), "attempt 2: bad creds")
	assert.Equal(t, http.StatusTooManyRequests, hit(), "attempt 3: throttled")
}

func TestBasicAuth_LimiterNotConsumedOnAbsentHeader(t *testing.T) {
	t.Parallel()
	// Missing header path returns 401 + WWW-Authenticate BEFORE the
	// limiter is consulted — otherwise a browser's first hit would
	// spuriously consume a token. burst=1 makes the assertion sharp.
	lim := auth.NewIPLimiter(rate.Every(time.Hour), 1)
	repo := seedBasicRepo(t, "admin", "hunter22")
	r := setupAuthBasic(t, "k", repo, lim, &AuthRuntime{Mode: runtime.AuthModeBasic})

	for range 3 {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, `Basic realm="Seasonfill"`, w.Header().Get("WWW-Authenticate"))
	}
	// Now a real bad-creds attempt must still be allowed once.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("Authorization", basicHeader("admin", "wrong"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "limiter should not have been pre-consumed")
}
