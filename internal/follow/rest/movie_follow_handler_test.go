package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	followapp "github.com/alexmorbo/seasonfill/internal/follow/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeUsers is the narrow ports.UserRepository stand-in: only GetByUsername +
// FirstAdminID are exercised by the handler's callerID resolution.
type fakeUsers struct {
	byName  map[string]admin.User
	adminID int64
	err     error
}

func (f *fakeUsers) Get(context.Context) (admin.User, error) { return admin.User{}, f.err }
func (f *fakeUsers) GetByUsername(_ context.Context, username string) (admin.User, error) {
	if u, ok := f.byName[username]; ok {
		return u, nil
	}
	return admin.User{}, ports.ErrNotFound
}
func (f *fakeUsers) FirstAdminID(context.Context) (int64, error) { return f.adminID, f.err }
func (f *fakeUsers) GetByOIDCSubject(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (f *fakeUsers) GetByJellyfinUserID(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (f *fakeUsers) Create(context.Context, admin.User) error { return nil }
func (f *fakeUsers) CreateFromOIDC(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (f *fakeUsers) CreateFromJellyfin(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (f *fakeUsers) UpdatePassword(context.Context, uint, string) error { return nil }
func (f *fakeUsers) UpdateSettings(context.Context, uint, ports.UserSettingsPatch) error {
	return nil
}
func (f *fakeUsers) UpdateLastLoginAt(context.Context, uint, time.Time) error { return nil }

type fakeMovieFollowSvc struct {
	followUser   int64
	followTMDB   domain.TMDBID
	followErr    error
	unfollowUser int64
	unfollowTMDB domain.TMDBID
	listUser     int64
	listLang     string
	listItems    []followapp.FollowedMovieItem
}

func (f *fakeMovieFollowSvc) Follow(_ context.Context, uid int64, id domain.TMDBID) error {
	f.followUser, f.followTMDB = uid, id
	return f.followErr
}

func (f *fakeMovieFollowSvc) Unfollow(_ context.Context, uid int64, id domain.TMDBID) error {
	f.unfollowUser, f.unfollowTMDB = uid, id
	return nil
}

func (f *fakeMovieFollowSvc) ListFollowed(_ context.Context, uid int64, lang string) ([]followapp.FollowedMovieItem, error) {
	f.listUser, f.listLang = uid, lang
	return f.listItems, nil
}

// newMovieRouter mounts the three movie-follow routes with `username` already
// injected into the gin context, standing in for the auth middleware.
func newMovieRouter(h *MovieFollowHandler, username string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if username != "" {
			c.Set(middleware.UsernameContextKey, username)
		}
		c.Next()
	})
	r.POST("/follow/movies", h.Post)
	r.DELETE("/follow/movies/:tmdb_id", h.Delete)
	r.GET("/follow/movies", h.List)
	return r
}

func testUsers() *fakeUsers {
	return &fakeUsers{
		byName: map[string]admin.User{
			"alice": {ID: 7, Username: "alice"},
			"bob":   {ID: 9, Username: "bob"},
		},
		adminID: 1,
	}
}

func do(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), method, path, rdr))
	return w
}

func TestMovieFollowHandler_Post_ScopesToCaller(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "bob")

	w := do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 550})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(9), svc.followUser, "caller id comes from the session, never the body")
	assert.Equal(t, domain.TMDBID(550), svc.followTMDB)
}

func TestMovieFollowHandler_Post_ApiKeyResolvesSeedAdmin(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "api-key")

	w := do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 12})

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(1), svc.followUser)
}

func TestMovieFollowHandler_Unauthenticated(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "")

	assert.Equal(t, http.StatusUnauthorized,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 1}).Code)
	assert.Equal(t, http.StatusUnauthorized,
		do(r, http.MethodDelete, "/follow/movies/1", nil).Code)
	assert.Equal(t, http.StatusUnauthorized,
		do(r, http.MethodGet, "/follow/movies", nil).Code)
	assert.Equal(t, int64(0), svc.followUser, "use case is never reached")
}

func TestMovieFollowHandler_UnknownSessionUserIs401(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "ghost")

	assert.Equal(t, http.StatusUnauthorized,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 1}).Code)
}

func TestMovieFollowHandler_Post_RejectsBadTMDBID(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	assert.Equal(t, http.StatusBadRequest,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 0}).Code)
	assert.Equal(t, http.StatusBadRequest,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": -5}).Code)
	assert.Equal(t, domain.TMDBID(0), svc.followTMDB)
}

func TestMovieFollowHandler_Post_MapsNotFound(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{followErr: followapp.ErrMovieNotFound}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	assert.Equal(t, http.StatusNotFound,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 99}).Code)
}

func TestMovieFollowHandler_Delete_ScopesToCaller(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	w := do(r, http.MethodDelete, "/follow/movies/550", nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), svc.unfollowUser)
	assert.Equal(t, domain.TMDBID(550), svc.unfollowTMDB)
}

func TestMovieFollowHandler_Delete_RejectsBadPathID(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	assert.Equal(t, http.StatusBadRequest, do(r, http.MethodDelete, "/follow/movies/abc", nil).Code)
	assert.Equal(t, http.StatusBadRequest, do(r, http.MethodDelete, "/follow/movies/0", nil).Code)
}

func TestMovieFollowHandler_List_RendersOwnItems(t *testing.T) {
	t.Parallel()
	tmdb := domain.TMDBID(550)
	poster := "/p.jpg"
	year := 1999
	svc := &fakeMovieFollowSvc{listItems: []followapp.FollowedMovieItem{{
		MovieID:     42,
		TMDBID:      &tmdb,
		Title:       "Fight Club",
		PosterAsset: &poster,
		Year:        &year,
		FollowedAt:  time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}}}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "bob")

	w := do(r, http.MethodGet, "/follow/movies?lang=ru-RU", nil)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(9), svc.listUser)
	assert.Equal(t, "ru-RU", svc.listLang)

	var got followedMovieListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, int64(42), got.Items[0].MovieID)
	require.NotNil(t, got.Items[0].TMDBID)
	assert.Equal(t, int64(550), *got.Items[0].TMDBID)
	assert.Equal(t, "Fight Club", got.Items[0].Title)
	assert.Equal(t, "2026-08-20T12:00:00Z", got.Items[0].FollowedAt)
}

func TestMovieFollowHandler_List_DefaultsLangToEnUS(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	require.Equal(t, http.StatusOK, do(r, http.MethodGet, "/follow/movies", nil).Code)
	assert.Equal(t, "en-US", svc.listLang)
}

func TestMovieFollowHandler_List_EmptyRendersEmptyArray(t *testing.T) {
	t.Parallel()
	svc := &fakeMovieFollowSvc{}
	r := newMovieRouter(NewMovieFollowHandler(svc, testUsers(), nil), "alice")

	w := do(r, http.MethodGet, "/follow/movies", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"items":[]}`, w.Body.String())
}

type fakeSeriesFollowSvc struct {
	followID   domain.SeriesID
	unfollowID domain.SeriesID
	listCalls  int
}

func (f *fakeSeriesFollowSvc) Follow(_ context.Context, _ int64, id domain.SeriesID) error {
	f.followID = id
	return nil
}

func (f *fakeSeriesFollowSvc) Unfollow(_ context.Context, _ int64, id domain.SeriesID) error {
	f.unfollowID = id
	return nil
}

func (f *fakeSeriesFollowSvc) ListFollowed(context.Context, int64, string) ([]followapp.FollowedItem, error) {
	f.listCalls++
	return nil, nil
}

// TestFollowRoutes_MovieRoutesDoNotShadowSeries mounts the exact route set
// server.go registers — the series `/follow/:series_id` wildcard alongside the
// static `/follow/movies/...` sibling — and proves each request still lands on
// its own handler. Guards the ADR-0022 Wave-3 addition against silently
// hijacking the shipped series-follow surface.
func TestFollowRoutes_MovieRoutesDoNotShadowSeries(t *testing.T) {
	t.Parallel()
	seriesSvc := &fakeSeriesFollowSvc{}
	movieSvc := &fakeMovieFollowSvc{}
	users := testUsers()
	sh := NewFollowHandler(seriesSvc, users, nil)
	mh := NewMovieFollowHandler(movieSvc, users, nil)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.UsernameContextKey, "alice"); c.Next() })
	r.POST("/follow", sh.Post)
	r.DELETE("/follow/:series_id", sh.Delete)
	r.GET("/follow", sh.List)
	r.POST("/follow/movies", mh.Post)
	r.DELETE("/follow/movies/:tmdb_id", mh.Delete)
	r.GET("/follow/movies", mh.List)

	require.Equal(t, http.StatusOK, do(r, http.MethodPost, "/follow", map[string]int{"series_id": 140}).Code)
	assert.Equal(t, domain.SeriesID(140), seriesSvc.followID)
	assert.Equal(t, domain.TMDBID(0), movieSvc.followTMDB, "series POST must not reach the movie handler")

	require.Equal(t, http.StatusOK, do(r, http.MethodDelete, "/follow/140", nil).Code)
	assert.Equal(t, domain.SeriesID(140), seriesSvc.unfollowID)

	require.Equal(t, http.StatusOK, do(r, http.MethodDelete, "/follow/movies/550", nil).Code)
	assert.Equal(t, domain.TMDBID(550), movieSvc.unfollowTMDB)
	assert.Equal(t, domain.SeriesID(140), seriesSvc.unfollowID, "movie DELETE must not reach the series handler")

	require.Equal(t, http.StatusOK, do(r, http.MethodGet, "/follow", nil).Code)
	assert.Equal(t, 1, seriesSvc.listCalls)
	require.Equal(t, http.StatusOK, do(r, http.MethodGet, "/follow/movies", nil).Code)
	assert.Equal(t, 1, seriesSvc.listCalls, "movie list must not reach the series handler")
}

func TestMovieFollowHandler_NotWired(t *testing.T) {
	t.Parallel()
	r := newMovieRouter(NewMovieFollowHandler(nil, testUsers(), nil), "alice")

	assert.Equal(t, http.StatusInternalServerError,
		do(r, http.MethodPost, "/follow/movies", map[string]int{"tmdb_id": 1}).Code)
	assert.Equal(t, http.StatusInternalServerError,
		do(r, http.MethodDelete, "/follow/movies/1", nil).Code)
	assert.Equal(t, http.StatusInternalServerError,
		do(r, http.MethodGet, "/follow/movies", nil).Code)
}
