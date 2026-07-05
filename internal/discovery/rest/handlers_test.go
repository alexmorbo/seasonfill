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
func (f *fakeRepo) HasAnyList(_ context.Context) (bool, error) {
	return false, nil
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
	return newTestHandlerWithLib(t, repo, warming, refresh, nil)
}

func newTestHandlerWithLib(t *testing.T, repo discoapp.DiscoveryListRepo,
	warming discoapp.WarmingProbe, refresh discoapp.RefreshOnDemand,
	lib discoapp.LibraryInstancesPort,
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
		nil, // resolver — story 526; nil-OK (raw TMDB paths flow unchanged)
		lib, // libraryInstances — story 527; nil-OK
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

// fakeLibraryInstances implements discoapp.LibraryInstancesPort.
// Records calls + returns a canned map. err overrides the success
// path; if err != nil ListBy returns the error.
type fakeLibraryInstances struct {
	mu     sync.Mutex
	calls  int
	result map[shareddomain.SeriesID][]string
	err    error
}

func (f *fakeLibraryInstances) ListByCanonicalSeriesIDs(_ context.Context, _ []shareddomain.SeriesID) (map[shareddomain.SeriesID][]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
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

// TestTrending_ProjectsTVDBIDAndOriginalLanguage — story 523. The
// curated trending pipeline reads disco.Item rows whose TVDBID +
// OriginalLanguage pointer fields carry through the projection into
// the JSON response so the FE AddToSonarr modal can submit without
// a second round-trip. Verifies both the populated and the nil cases
// on the same response so the `omitempty` branch is exercised too.
func TestTrending_ProjectsTVDBIDAndOriginalLanguage(t *testing.T) {
	repo := newFakeRepo()
	tvdb := shareddomain.TVDBID(81189)
	ol := "en"
	items := []disco.Item{
		{
			SeriesID:         shareddomain.SeriesID(1),
			Title:            "With TVDB",
			TVDBID:           &tvdb,
			OriginalLanguage: &ol,
		},
		{
			SeriesID: shareddomain.SeriesID(2),
			Title:    "Legacy Stub",
		},
	}
	repo.setPage(disco.KindTrendingDay, "", "en-US", disco.Page{
		Items: items, RefreshedAt: time.Now(), Total: len(items),
	}, false, time.Now())

	r := newTestHandler(t, repo, &fakeWarming{}, &fakeRefresh{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/trending?scope=day&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)

	require.NotNil(t, resp.Items[0].TVDBID,
		"populated TVDBID must surface in JSON")
	require.Equal(t, 81189, *resp.Items[0].TVDBID)
	require.NotNil(t, resp.Items[0].OriginalLanguage,
		"populated OriginalLanguage must surface in JSON")
	require.Equal(t, "en", *resp.Items[0].OriginalLanguage)

	require.Nil(t, resp.Items[1].TVDBID,
		"nil domain TVDBID → nil DTO field (omitempty kicks in)")
	require.Nil(t, resp.Items[1].OriginalLanguage,
		"nil domain OriginalLanguage → nil DTO field")

	// Spot-check that `omitempty` actually drops the keys for the
	// nil row — otherwise the FE will receive `"tvdb_id":null` and
	// the `typeof tvdb_id === 'number'` guard misfires.
	body := rec.Body.String()
	require.Contains(t, body, `"tvdb_id":81189`)
	require.NotContains(t, body, `"tvdb_id":null`)
	require.NotContains(t, body, `"original_language":null`)
}

// Story 1036 — projectItem must surface domain Item.TMDBRating + Year
// on the wire so the unified FE card renders ★rating + year. A nil
// rating (TMDB gave no vote) drops the key via omitempty.
func TestTrending_ProjectsTMDBRatingAndYear_Story1036(t *testing.T) {
	repo := newFakeRepo()
	rating := 8.4
	year := 2021
	items := []disco.Item{
		{
			SeriesID:   shareddomain.SeriesID(1),
			Title:      "Rated",
			TMDBRating: &rating,
			Year:       &year,
		},
		{
			SeriesID: shareddomain.SeriesID(2),
			Title:    "Unrated",
		},
	}
	repo.setPage(disco.KindTrendingDay, "", "en-US", disco.Page{
		Items: items, RefreshedAt: time.Now(), Total: len(items),
	}, false, time.Now())

	r := newTestHandler(t, repo, &fakeWarming{}, &fakeRefresh{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/trending?scope=day&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)

	require.NotNil(t, resp.Items[0].TMDBRating, "populated rating must surface")
	require.InDelta(t, 8.4, *resp.Items[0].TMDBRating, 1e-9)
	require.NotNil(t, resp.Items[0].Year, "populated year must surface")
	require.Equal(t, 2021, *resp.Items[0].Year)

	require.Nil(t, resp.Items[1].TMDBRating, "nil rating → nil DTO field")
	require.Nil(t, resp.Items[1].Year, "nil year → nil DTO field")

	body := rec.Body.String()
	require.Contains(t, body, `"tmdb_rating":8.4`)
	require.NotContains(t, body, `"tmdb_rating":null`)
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

// Story 527: with libraryInstances wired, projectItem populates
// in_library_instances from the batched lookup result.
func TestTrending_InLibraryInstances_Populated(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindTrendingDay, "", "en-US", 3)
	lib := &fakeLibraryInstances{
		result: map[shareddomain.SeriesID][]string{
			1: {"homelab"},
			2: {"alpha", "beta"},
			// 3: absent → must render []
		},
	}
	r := newTestHandlerWithLib(t, repo, &fakeWarming{}, &fakeRefresh{}, lib)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/trending?scope=day&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	require.Equal(t, []string{"homelab"}, resp.Items[0].InLibraryInstances)
	require.Equal(t, []string{"alpha", "beta"}, resp.Items[1].InLibraryInstances)
	require.Equal(t, []string{}, resp.Items[2].InLibraryInstances)

	lib.mu.Lock()
	calls := lib.calls
	lib.mu.Unlock()
	require.Equal(t, 1, calls, "MUST be one batched lookup per response (no N+1)")
}

// Story 527: nil port keeps the legacy []string{} shape — no panic,
// no SQL, no warn log.
func TestTrending_InLibrary_NilPort_LegacyShape(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindTrendingDay, "", "en-US", 2)
	r := newTestHandlerWithLib(t, repo, &fakeWarming{}, &fakeRefresh{}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/trending?scope=day&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)
	for _, it := range resp.Items {
		require.Equal(t, []string{}, it.InLibraryInstances,
			"nil port MUST keep legacy []string{} shape")
	}
}

// Story 527: port error → 200 with legacy []string{} shape (graceful
// degrade). The bug regresses to "+ Add to Sonarr always visible"
// but the request MUST NOT 500.
func TestTrending_InLibrary_PortError_GracefulDegrade(t *testing.T) {
	repo := newFakeRepo()
	seedSeries(repo, disco.KindTrendingDay, "", "en-US", 2)
	lib := &fakeLibraryInstances{err: errors.New("db down")}
	r := newTestHandlerWithLib(t, repo, &fakeWarming{}, &fakeRefresh{}, lib)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/trending?scope=day&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "MUST NOT 500 on port error")
	var resp discoveryrest.DiscoveryListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	for _, it := range resp.Items {
		require.Equal(t, []string{}, it.InLibraryInstances)
	}
}
