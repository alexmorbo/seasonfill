package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeMoviePassthrough scripts the four Fetch* outcomes. delay+ctx models the
// Pattern-B sync-timeout branch.
type fakeMoviePassthrough struct {
	calls    atomic.Int64
	items    []disco.MovieItem
	err      error
	delay    time.Duration
	waitSecs float64
}

func (f *fakeMoviePassthrough) run(ctx context.Context) ([]disco.MovieItem, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func (f *fakeMoviePassthrough) FetchDiscover(ctx context.Context, _ tmdb.MovieDiscoverFilter, _ string, _ int) ([]disco.MovieItem, error) {
	return f.run(ctx)
}
func (f *fakeMoviePassthrough) FetchTrending(ctx context.Context, _ tmdb.TrendingScope, _ string, _ int) ([]disco.MovieItem, error) {
	return f.run(ctx)
}
func (f *fakeMoviePassthrough) FetchPopular(ctx context.Context, _ string, _ int) ([]disco.MovieItem, error) {
	return f.run(ctx)
}
func (f *fakeMoviePassthrough) FetchSearch(ctx context.Context, _, _ string, _ int) ([]disco.MovieItem, error) {
	return f.run(ctx)
}
func (f *fakeMoviePassthrough) LastWaitSeconds() float64 { return f.waitSecs }

func newMovieHarness(t *testing.T, pass discoapp.MovieTMDBPassthrough) (*gin.Engine, *cachewatch.Cache[string, []disco.MovieItem]) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sizer := func(k string, v []disco.MovieItem) int { return len(k) + len(v)*500 }
	lru := cachewatch.New[string, []disco.MovieItem]("movie_discover_test_"+t.Name(), 8, time.Hour, sizer)
	t.Cleanup(func() { _ = lru.Close() })
	h := discoveryrest.NewMovieDiscoverHandler(lru, pass, nil, log)
	r := gin.New()
	r.GET("/discovery/movie/discover", h.Discover)
	r.GET("/discovery/movie/trending", h.Trending)
	r.GET("/discovery/movie/popular", h.Popular)
	r.GET("/discovery/movie/search", h.Search)
	return r, lru
}

func doGet(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	r.ServeHTTP(w, req)
	return w
}

func TestMovieDiscover_MissSyncThenHit(t *testing.T) {
	pass := &fakeMoviePassthrough{items: []disco.MovieItem{{MovieID: 1, Title: "Dune", TMDBID: new(shareddomain.TMDBID(693134))}}}
	r, _ := newMovieHarness(t, pass)

	// First call → miss_sync (200, cache_status=miss).
	w := doGet(t, r, "/discovery/movie/discover?lang=en-US")
	require.Equal(t, http.StatusOK, w.Code)
	var body discoveryrest.MovieDiscoverResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "miss", body.CacheStatus)
	require.Len(t, body.Items, 1)
	require.Equal(t, "Dune", body.Items[0].Title)
	require.EqualValues(t, 1, pass.calls.Load())

	// Second identical call → hit (served from LRU, no extra fetch).
	w2 := doGet(t, r, "/discovery/movie/discover?lang=en-US")
	require.Equal(t, http.StatusOK, w2.Code)
	var body2 discoveryrest.MovieDiscoverResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &body2))
	require.Equal(t, "hit", body2.CacheStatus)
	require.EqualValues(t, 1, pass.calls.Load(), "hit must not re-fetch")
}

func TestMovieDiscover_MissWarming_202(t *testing.T) {
	pass := &fakeMoviePassthrough{delay: 30 * time.Second} // exceeds 5s sync timeout
	r, _ := newMovieHarness(t, pass)
	w := doGet(t, r, "/discovery/movie/discover")
	require.Equal(t, http.StatusAccepted, w.Code)
	var body discoveryrest.MovieDiscoverResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "warming", body.CacheStatus)
	require.Contains(t, body.Degraded, "tmdb_throttled")
}

func TestMovieDiscover_UpstreamError_502(t *testing.T) {
	pass := &fakeMoviePassthrough{err: errors.New("tmdb down")}
	r, _ := newMovieHarness(t, pass)
	w := doGet(t, r, "/discovery/movie/discover")
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestMovieDiscover_ParseValidation_400(t *testing.T) {
	pass := &fakeMoviePassthrough{items: []disco.MovieItem{}}
	r, _ := newMovieHarness(t, pass)
	for _, q := range []string{
		"/discovery/movie/discover?page=0",
		"/discovery/movie/discover?page=501",
		"/discovery/movie/discover?lang=zzzz",
		"/discovery/movie/discover?sort_by=garbage",
		"/discovery/movie/discover?with_release_type=9",
		"/discovery/movie/discover?vote_average.gte=99",
		"/discovery/movie/discover?primary_release_year=99999",
	} {
		w := doGet(t, r, q)
		require.Equalf(t, http.StatusBadRequest, w.Code, "query %q should 400", q)
	}
}

func TestMovieTrending_ScopeValidation(t *testing.T) {
	pass := &fakeMoviePassthrough{items: []disco.MovieItem{}}
	r, _ := newMovieHarness(t, pass)
	require.Equal(t, http.StatusBadRequest, doGet(t, r, "/discovery/movie/trending?scope=month").Code)
	require.Equal(t, http.StatusOK, doGet(t, r, "/discovery/movie/trending?scope=week").Code)
}

func TestMoviePopularAndSearch_SyncPaths(t *testing.T) {
	pass := &fakeMoviePassthrough{items: []disco.MovieItem{{MovieID: 2, Title: "Sicario"}}}
	r, _ := newMovieHarness(t, pass)

	wp := doGet(t, r, "/discovery/movie/popular")
	require.Equal(t, http.StatusOK, wp.Code)

	// search requires q.
	require.Equal(t, http.StatusBadRequest, doGet(t, r, "/discovery/movie/search").Code)
	ws := doGet(t, r, "/discovery/movie/search?q=sicario")
	require.Equal(t, http.StatusOK, ws.Code)
	var body discoveryrest.MovieDiscoverResponse
	require.NoError(t, json.Unmarshal(ws.Body.Bytes(), &body))
	require.Len(t, body.Items, 1)
}
