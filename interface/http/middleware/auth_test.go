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

var ginOnce sync.Once

func setupAuth(apiKey string) *gin.Engine {
	ginOnce.Do(func() { gin.SetMode(gin.TestMode) })
	r := gin.New()
	api := r.Group("/api")
	api.Use(RequireAuth(apiKey))
	api.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user": c.GetString(UsernameContextKey)})
	})
	return r
}

func TestRequireAuth_ValidAPIKey(t *testing.T) {
	t.Parallel()
	r := setupAuth("secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.Header.Set("X-Api-Key", "secret")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAuth_ValidCookie(t *testing.T) {
	t.Parallel()
	tok, err := SignSession([]byte("secret"), "admin", time.Now().Add(time.Hour))
	require.NoError(t, err)
	r := setupAuth("secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"user":"admin"`)
}

func TestRequireAuth_BothFail_401(t *testing.T) {
	t.Parallel()
	r := setupAuth("secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "junk"})
	req.Header.Set("X-Api-Key", "wrong")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_NoAuth_401(t *testing.T) {
	t.Parallel()
	r := setupAuth("secret")
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAuth_IdenticalRejection(t *testing.T) {
	t.Parallel()
	r := setupAuth("secret")
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
