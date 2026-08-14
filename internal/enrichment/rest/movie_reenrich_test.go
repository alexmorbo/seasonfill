package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeMarker records calls and returns a canned count/err.
type fakeMarker struct {
	count int64
	err   error
	calls int
}

func (f *fakeMarker) MarkAllMoviesChanged(_ context.Context, _ time.Time) (int64, error) {
	f.calls++
	return f.count, f.err
}

// stubUsers satisfies dataports.UserRepository via embedding (unused methods
// stay nil — only GetByUsername is exercised by RequirePermission). A miss
// returns a plain not-found error, which the guard maps to 403.
type stubUsers struct {
	dataports.UserRepository
	byName map[string]admin.User
}

func (s stubUsers) GetByUsername(_ context.Context, name string) (admin.User, error) {
	if u, ok := s.byName[name]; ok {
		return u, nil
	}
	return admin.User{}, errors.Join(errors.New("user not found"), dataports.ErrNotFound)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// buildReenrichRouter wires the real permAdmin-equivalent guard + handler.
// principal is seeded into the gin context (mirror of RequireAuthWithRuntime);
// empty principal means "no authenticated user".
func buildReenrichRouter(t *testing.T, marker MovieChangeMarker, users dataports.UserRepository, principal string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewMovieReenrichHandler(marker, discardLogger())
	guard := middleware.RequirePermission(users, middleware.PermManageUsers, middleware.PermManageRequests)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if principal != "" {
			c.Set(middleware.UsernameContextKey, principal)
		}
		c.Next()
	})
	r.POST("/api/v1/admin/movies/reenrich", guard, h.Trigger)
	return r
}

func doReenrich(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/admin/movies/reenrich", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMovieReenrich_Admin_200_WithCount(t *testing.T) {
	marker := &fakeMarker{count: 411}
	users := stubUsers{byName: map[string]admin.User{
		"alex": {ID: 1, Username: "alex", Role: admin.RoleAdmin},
	}}
	r := buildReenrichRouter(t, marker, users, "alex")

	w := doReenrich(t, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body movieReenrichResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(411), body.Marked)
	assert.Equal(t, 1, marker.calls)
}

func TestMovieReenrich_ApiKey_200(t *testing.T) {
	// The "api-key" automation principal short-circuits the guard with no DB
	// lookup — an empty user repo is fine.
	marker := &fakeMarker{count: 7}
	users := stubUsers{byName: map[string]admin.User{}}
	r := buildReenrichRouter(t, marker, users, "api-key")

	w := doReenrich(t, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body movieReenrichResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, int64(7), body.Marked)
	assert.Equal(t, 1, marker.calls)
}

func TestMovieReenrich_Unauthenticated_401_NoMark(t *testing.T) {
	marker := &fakeMarker{count: 411}
	users := stubUsers{byName: map[string]admin.User{}}
	r := buildReenrichRouter(t, marker, users, "") // no principal seeded

	w := doReenrich(t, r)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, 0, marker.calls, "guard must reject before the handler runs")
}

func TestMovieReenrich_NonAdminNoPerms_403_NoMark(t *testing.T) {
	marker := &fakeMarker{count: 411}
	// A plain user with none of the manage_* perms.
	users := stubUsers{byName: map[string]admin.User{
		"bob": {ID: 2, Username: "bob", Role: admin.RoleUser},
	}}
	r := buildReenrichRouter(t, marker, users, "bob")

	w := doReenrich(t, r)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 0, marker.calls, "guard must reject before the handler runs")
}

func TestMovieReenrich_MarkerError_500(t *testing.T) {
	marker := &fakeMarker{err: errors.New("db down")}
	users := stubUsers{byName: map[string]admin.User{
		"alex": {ID: 1, Username: "alex", Role: admin.RoleAdmin},
	}}
	r := buildReenrichRouter(t, marker, users, "alex")

	w := doReenrich(t, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, 1, marker.calls)
}

func TestNewMovieReenrichHandler_NilMarker_Panics(t *testing.T) {
	assert.Panics(t, func() { NewMovieReenrichHandler(nil, discardLogger()) })
}
