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
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeSearchRepo is the unit-test fake for discoapp.SearchRepo.
type fakeSearchRepo struct {
	items []disco.Item
	err   error
	calls atomic.Int64
	lang  atomic.Value // last language arg
}

func (f *fakeSearchRepo) LocalSearch(_ context.Context, _ string, lang string, _ int) ([]disco.Item, error) {
	f.calls.Add(1)
	f.lang.Store(lang)
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

// fakeSearchTMDB is the unit-test fake for discoapp.SearchTMDB.
type fakeSearchTMDB struct {
	resp  *tmdb.TVListResponse
	err   error
	calls atomic.Int64
}

func (f *fakeSearchTMDB) SearchTV(_ context.Context, _, _ string, _ int) (*tmdb.TVListResponse, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// fakeStubs implements discoapp.StubUpserter; counts EnsureStub calls
// and assigns deterministic SeriesIDs starting at 1001.
type fakeStubs struct {
	nextID atomic.Int64
	calls  atomic.Int64
}

func (f *fakeStubs) EnsureStub(_ context.Context, _ shareddomain.TMDBID, _, _, _, _ string, _, _ *string) (shareddomain.SeriesID, error) {
	f.calls.Add(1)
	if f.nextID.Load() == 0 {
		f.nextID.Store(1000)
	}
	return shareddomain.SeriesID(f.nextID.Add(1)), nil
}

// fakeDispatch records enqueue calls.
type fakeDispatch struct {
	enqueues atomic.Int64
	lastID   atomic.Int64
	lastKind atomic.Value
	lastPri  atomic.Value
}

func (f *fakeDispatch) Enqueue(entity string, id int64, priority string) {
	f.enqueues.Add(1)
	f.lastID.Store(id)
	f.lastKind.Store(entity)
	f.lastPri.Store(priority)
}

func newSearchHandler(t *testing.T, repo discoapp.SearchRepo, tm discoapp.SearchTMDB,
	stubs discoapp.StubUpserter, disp discoapp.EnrichmentDispatcher,
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	uc := discoapp.NewSearchUseCase(repo, tm, stubs, disp, log)
	h := discoveryrest.NewDiscoveryHandler(
		newFakeRepo(),
		&fakeWarming{},
		&fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		uc,
		nil, // resolver — story 526; nil-OK
		nil, // libraryInstances — story 527; nil-OK
		log,
	)
	r := gin.New()
	r.GET("/discovery/search", h.Search)
	return r
}

func TestSearch_LocalHit_ReturnsSourceLocal(t *testing.T) {
	repo := &fakeSearchRepo{items: []disco.Item{
		{SeriesID: 1, Title: "Rick and Morty"},
		{SeriesID: 2, Title: "Breaking Bad"},
		{SeriesID: 3, Title: "Better Call Saul"},
	}}
	tm := &fakeSearchTMDB{}
	stubs := &fakeStubs{}
	disp := &fakeDispatch{}
	r := newSearchHandler(t, repo, tm, stubs, disp)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=Rick&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items  []discoveryrest.DiscoverySeriesItem `json:"items"`
		Source string                              `json:"source"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 3)
	require.Equal(t, "local", resp.Source)
	require.Equal(t, int64(0), tm.calls.Load(), "TMDB must not be hit on local hit")
	require.Equal(t, int64(0), disp.enqueues.Load())
	require.Equal(t, "en-US", repo.lang.Load())
}

func TestSearch_LocalMiss_TMDBFallback(t *testing.T) {
	repo := &fakeSearchRepo{items: nil} // empty
	tm := &fakeSearchTMDB{resp: &tmdb.TVListResponse{
		Results: []tmdb.TVListEntry{
			{ID: 100, Name: "Obscure Show", FirstAirDate: "2024-01-01"},
			{ID: 200, Name: "Another Show", FirstAirDate: "2020-06-15"},
			{ID: 300, Name: "Third Result"},
			{ID: 400, Name: "Fourth"},
			{ID: 500, Name: "Fifth"},
		},
	}}
	stubs := &fakeStubs{}
	disp := &fakeDispatch{}
	r := newSearchHandler(t, repo, tm, stubs, disp)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=ZzzObscure&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items  []discoveryrest.DiscoverySeriesItem `json:"items"`
		Source string                              `json:"source"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 5)
	require.Equal(t, "tmdb", resp.Source)
	require.Equal(t, int64(1), tm.calls.Load())
	require.Equal(t, int64(5), stubs.calls.Load())
	require.Equal(t, int64(5), disp.enqueues.Load())
	require.Equal(t, "series", disp.lastKind.Load())
	require.Equal(t, "hot", disp.lastPri.Load())
}

func TestSearch_EmptyQuery_400(t *testing.T) {
	r := newSearchHandler(t, &fakeSearchRepo{}, &fakeSearchTMDB{}, &fakeStubs{}, &fakeDispatch{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/search?q=", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"invalid_query"`)
}

func TestSearch_QueryTooLong_400(t *testing.T) {
	r := newSearchHandler(t, &fakeSearchRepo{}, &fakeSearchTMDB{}, &fakeStubs{}, &fakeDispatch{})
	q := strings.Repeat("a", 101)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/discovery/search?q="+q, nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"invalid_query"`)
}

func TestSearch_TMDBError_502(t *testing.T) {
	repo := &fakeSearchRepo{items: nil}
	tm := &fakeSearchTMDB{err: errors.New("tmdb 5xx")}
	r := newSearchHandler(t, repo, tm, &fakeStubs{}, &fakeDispatch{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=Anything&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"tmdb_unavailable"`)
}

func TestSearch_LocalRepoError_500(t *testing.T) {
	repo := &fakeSearchRepo{err: errors.New("db dead")}
	r := newSearchHandler(t, repo, &fakeSearchTMDB{}, &fakeStubs{}, &fakeDispatch{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=x&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"search_read_failed"`)
}

func TestSearch_InvalidLanguage_400(t *testing.T) {
	r := newSearchHandler(t, &fakeSearchRepo{}, &fakeSearchTMDB{}, &fakeStubs{}, &fakeDispatch{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=x&lang=not-a-bcp47", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"invalid_language"`)
}

func TestSearch_LangThreadedToRepo(t *testing.T) {
	repo := &fakeSearchRepo{items: []disco.Item{{SeriesID: 1, Title: "x"}}}
	r := newSearchHandler(t, repo, &fakeSearchTMDB{}, &fakeStubs{}, &fakeDispatch{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=x&lang=ru-RU", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ru-RU", repo.lang.Load())
}

func TestSearch_SearchUCNotWired_503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := discoveryrest.NewDiscoveryHandler(
		newFakeRepo(), &fakeWarming{}, &fakeRefresh{},
		persistence.NewGenresPickerRepo(nil),
		persistence.NewNetworksPickerRepo(nil),
		nil, nil, nil, log,
	)
	r := gin.New()
	r.GET("/discovery/search", h.Search)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET",
		"/discovery/search?q=x&lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), `"error":"search_unavailable"`)
}
