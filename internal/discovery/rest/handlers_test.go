package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
// Maps are guarded by mu so the concurrent singleflight test stays
// race-clean under -race.
type fakeRepo struct {
	mu          sync.Mutex
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
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pages[fakeKey(kind, param, lang)], nil
}
func (f *fakeRepo) IsStale(_ context.Context, kind disco.Kind, param, lang string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.stale[fakeKey(kind, param, lang)]
	if !ok {
		return true, nil
	}
	return v, nil
}
func (f *fakeRepo) LastRefreshedAt(_ context.Context, kind disco.Kind, param, lang string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastRefresh[fakeKey(kind, param, lang)], nil
}
func (f *fakeRepo) ReplaceList(_ context.Context, _ disco.Kind, _, _ string, _ []disco.Item) error {
	return nil
}

// setPage is a test-only helper that atomically writes the (kind, param,
// lang) tuple into pages/stale/lastRefresh under mu.
func (f *fakeRepo) setPage(kind disco.Kind, param, lang string, p disco.Page, stale bool, refresh time.Time) {
	k := fakeKey(kind, param, lang)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages[k] = p
	f.stale[k] = stale
	f.lastRefresh[k] = refresh
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
		f.repo.setPage(kind, param, lang, disco.Page{
			Items:       f.emit,
			RefreshedAt: f.refresh,
			Total:       len(f.emit),
		}, false, f.refresh)
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
		nil, // searchUC — story 508; nil-OK for curated-endpoint tests
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
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.pages[fakeKey(kind, param, lang)] = disco.Page{
		Items:       items,
		RefreshedAt: time.Now(),
		Total:       len(items),
	}
}

// setStale is a test-only helper that flips repo.stale[key] under mu.
func (f *fakeRepo) setStale(kind disco.Kind, param, lang string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stale[fakeKey(kind, param, lang)] = v
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

func TestByGenre_CachedHit_DoesNotCallRefresh(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindByGenre, "18", "en-US", 5)
	repo.setStale(disco.KindByGenre, "18", "en-US", false)
	rf := &fakeRefresh{repo: repo}
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/18?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(0), rf.calls.Load(), "fresh cache must NOT call RefreshNow")
}

func TestByGenre_Stale_TriggersRefresh(t *testing.T) {
	repo := newFakeRepo()
	rf := &fakeRefresh{
		repo: repo,
		emit: []disco.Item{
			{SeriesID: shareddomain.SeriesID(1), Title: "Refreshed"},
		},
		refresh: time.Now(),
	}
	repo.setStale(disco.KindByGenre, "18", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/18?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(1), rf.calls.Load())
	require.Contains(t, rec.Body.String(), `"Refreshed"`)
}

func TestByGenre_ConcurrentStaleRequests_SingleflightCollapses(t *testing.T) {
	repo := newFakeRepo()
	rf := &fakeRefresh{
		repo: repo,
		emit: []disco.Item{
			{SeriesID: shareddomain.SeriesID(1), Title: "Refreshed"},
		},
		refresh: time.Now(),
	}
	repo.setStale(disco.KindByGenre, "18", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	const n = 16
	done := make(chan struct{}, n)
	for range n {
		go func() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/18?lang=en-US", nil)
			r.ServeHTTP(rec, req)
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}
	// Singleflight collapses the burst to ONE RefreshNow call.
	require.Equal(t, int64(1), rf.calls.Load())
}

func TestByGenre_UnknownParam_RefreshOK_Empty_Degraded(t *testing.T) {
	repo := newFakeRepo()
	// Refresh "succeeds" but writes 0 items.
	rf := &fakeRefresh{repo: repo, emit: nil, refresh: time.Now()}
	repo.setStale(disco.KindByGenre, "99999", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/99999?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(t, resp.Items)
	require.Equal(t, []string{"genre_unknown_to_tmdb"}, resp.Degraded)
}

func TestByGenre_RefreshFails_NoCachedFallback_502(t *testing.T) {
	repo := newFakeRepo()
	rf := &fakeRefresh{repo: repo, err: errors.New("tmdb down")}
	repo.setStale(disco.KindByGenre, "18", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/18?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), `"discovery_unavailable"`)
}

func TestByGenre_RefreshFails_StaleFallback_200_Degraded(t *testing.T) {
	repo := newFakeRepo()
	// Pre-seed stale cache.
	seedSeries(repo, disco.KindByGenre, "18", "en-US", 3)
	repo.setStale(disco.KindByGenre, "18", "en-US", true)
	rf := &fakeRefresh{repo: repo, err: errors.New("tmdb down")}
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/genre/18?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"refresh_failed"`)
}

func TestByNetwork_StaleTrigger(t *testing.T) {
	repo := newFakeRepo()
	rf := &fakeRefresh{
		repo:    repo,
		emit:    []disco.Item{{SeriesID: shareddomain.SeriesID(7), Title: "Net"}},
		refresh: time.Now(),
	}
	repo.setStale(disco.KindByNetwork, "213", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/network/213?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(1), rf.calls.Load())
}

func TestByKeyword_StaleTrigger(t *testing.T) {
	repo := newFakeRepo()
	rf := &fakeRefresh{
		repo:    repo,
		emit:    []disco.Item{{SeriesID: shareddomain.SeriesID(9), Title: "Kw"}},
		refresh: time.Now(),
	}
	repo.setStale(disco.KindByKeyword, "33", "en-US", true)
	r := newTestHandler(t, repo, &fakeWarming{}, rf)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/keyword/33?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(1), rf.calls.Load())
}

func TestLongTail_InvalidID_400(t *testing.T) {
	r := newTestHandler(t, newFakeRepo(), &fakeWarming{}, &fakeRefresh{})
	for _, path := range []string{"/discovery/genre/abc", "/discovery/network/0", "/discovery/keyword/-1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET", path, nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}
