package rest_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeRepo implements discoapp.DiscoveryListRepo for handler tests.
type fakeRepo struct {
	pages       map[string]disco.Page
	stale       map[string]bool
	lastRefresh map[string]time.Time
	getCalls    atomic.Int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		pages:       map[string]disco.Page{},
		stale:       map[string]bool{},
		lastRefresh: map[string]time.Time{},
	}
}

func fakeKey(kind disco.Kind, param, lang string) string {
	return string(kind) + "|" + param + "|" + lang
}

func (f *fakeRepo) GetRanked(_ context.Context, kind disco.Kind, param, lang string, _, _ int) (disco.Page, error) {
	f.getCalls.Add(1)
	return f.pages[fakeKey(kind, param, lang)], nil
}
func (f *fakeRepo) IsStale(_ context.Context, kind disco.Kind, param, lang string, _ time.Duration) (bool, error) {
	v, ok := f.stale[fakeKey(kind, param, lang)]
	if !ok {
		return true, nil
	}
	return v, nil
}
func (f *fakeRepo) LastRefreshedAt(_ context.Context, kind disco.Kind, param, lang string) (time.Time, error) {
	return f.lastRefresh[fakeKey(kind, param, lang)], nil
}
func (f *fakeRepo) ReplaceList(_ context.Context, _ disco.Kind, _, _ string, _ []disco.Item) error {
	return nil
}

// fakeWarming implements discoapp.WarmingProbe.
type fakeWarming struct{ on atomic.Bool }

func (f *fakeWarming) IsWarming() bool { return f.on.Load() }

// fakeRefresh implements discoapp.RefreshOnDemand. Records calls.
type fakeRefresh struct {
	calls atomic.Int64
	err   error
	// After RefreshNow, write items into the paired repo so the
	// handler's post-refresh read sees fresh data.
	repo    *fakeRepo
	emit    []disco.Item
	refresh time.Time
}

func (f *fakeRefresh) RefreshNow(_ context.Context, kind disco.Kind, param, lang string) error {
	f.calls.Add(1)
	if f.err != nil {
		return f.err
	}
	if f.repo != nil {
		f.repo.pages[fakeKey(kind, param, lang)] = disco.Page{
			Items:       f.emit,
			RefreshedAt: f.refresh,
			Total:       len(f.emit),
		}
		f.repo.stale[fakeKey(kind, param, lang)] = false
		f.repo.lastRefresh[fakeKey(kind, param, lang)] = f.refresh
	}
	return nil
}

func newTestHandler(t *testing.T, repo discoapp.DiscoveryListRepo,
	warming discoapp.WarmingProbe, refresh discoapp.RefreshOnDemand,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := discoveryrest.NewDiscoveryHandler(
		repo, warming, refresh,
		// Picker repos are unused for the unit slice; pass non-nil
		// stub repos so the constructor doesn't panic.
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		log,
	)
	r := gin.New()
	r.GET("/discovery/trending", h.Trending)
	r.GET("/discovery/popular", h.Popular)
	r.GET("/discovery/genre/:id", h.ByGenre)
	r.GET("/discovery/network/:id", h.ByNetwork)
	r.GET("/discovery/keyword/:id", h.ByKeyword)
	return r
}

func seedSeries(repo *fakeRepo, kind disco.Kind, param, lang string, n int) {
	items := make([]disco.Item, 0, n)
	for i := 1; i <= n; i++ {
		t := "Series " + strings.Repeat("x", i)
		id := shareddomain.SeriesID(i)
		items = append(items, disco.Item{
			SeriesID: id,
			Title:    t,
		})
	}
	repo.pages[fakeKey(kind, param, lang)] = disco.Page{
		Items:       items,
		RefreshedAt: time.Now(),
		Total:       len(items),
	}
}

func TestTrending_HappyPath_DayAndWeek(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindTrendingDay, "", "en-US", 20)
	seedSeries(repo, disco.KindTrendingWeek, "", "en-US", 15)
	r := newTestHandler(t, repo, &fakeWarming{}, &fakeRefresh{})

	cases := []struct {
		scope string
		count int
	}{{"day", 20}, {"week", 15}}
	for _, tc := range cases {
		t.Run(tc.scope, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), "GET",
				"/discovery/trending?scope="+tc.scope+"&lang=en-US", nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var resp discoveryrest.DiscoveryListResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Items, tc.count)
			require.Equal(t, 1, resp.Page)
			require.Equal(t, 20, resp.PerPage)
		})
	}
}

func TestTrending_InvalidScope_400(t *testing.T) {
	r := newTestHandler(t, newFakeRepo(), &fakeWarming{}, &fakeRefresh{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/trending?scope=year", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"invalid_scope"`)
}

func TestTrending_Warming_ReturnsDegradedEnvelope(t *testing.T) {
	w := &fakeWarming{}
	w.on.Store(true)
	r := newTestHandler(t, newFakeRepo(), w, &fakeRefresh{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/trending?scope=day", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(t, resp.Items)
	require.Equal(t, []string{"discovery_warming"}, resp.Degraded)
	require.NotNil(t, resp.WarmingEst)
	require.Equal(t, 30, *resp.WarmingEst)
}

func TestPopular_HappyPath(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindPopular, "", "en-US", 12)
	r := newTestHandler(t, repo, &fakeWarming{}, &fakeRefresh{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/popular?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestParsePaging_Boundaries(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindPopular, "", "en-US", 1)
	r := newTestHandler(t, repo, &fakeWarming{}, &fakeRefresh{})
	cases := []struct {
		q      string
		status int
		slug   string
	}{
		{"page=0", http.StatusBadRequest, "invalid_page"},
		{"page=51", http.StatusBadRequest, "invalid_page"},
		{"page=abc", http.StatusBadRequest, "invalid_page"},
		{"per_page=0", http.StatusBadRequest, "invalid_per_page"},
		{"per_page=200", http.StatusOK, ""}, // clamps to 100, returns 200
		{"lang=zzzz-zzzz-zzzz", http.StatusBadRequest, "invalid_language"},
	}
	for _, tc := range cases {
		t.Run(tc.q, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/popular?"+tc.q, nil)
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code)
			if tc.slug != "" {
				require.Contains(t, rec.Body.String(), `"error":"`+tc.slug+`"`)
			}
		})
	}
}

func TestLongTail_Commit_A_Stub_Returns_501(t *testing.T) {
	r := newTestHandler(t, newFakeRepo(), &fakeWarming{}, &fakeRefresh{})
	for _, path := range []string{"/discovery/genre/1", "/discovery/network/1", "/discovery/keyword/1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET", path, nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotImplemented, rec.Code, path)
	}
}
