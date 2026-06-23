package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// --- fakes ---

type fakeGlobalCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (f *fakeGlobalCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

type fakeGlobalComposerDelegate struct {
	resp *seriesdetail.Detail
	err  error
}

func (f *fakeGlobalComposerDelegate) Get(_ context.Context, inst domain.InstanceName, sid domain.SonarrSeriesID, _ string) (*seriesdetail.Detail, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.resp == nil {
		return &seriesdetail.Detail{
			Instance:       inst,
			SonarrSeriesID: sid,
			SeriesID:       140,
			Canon:          series.Canon{Title: "Rick and Morty"},
		}, nil
	}
	return f.resp, nil
}

type fakeGlobalTMDBFallback struct {
	resp *seriesdetail.Detail
	err  error
}

func (f *fakeGlobalTMDBFallback) GetCanonical(_ context.Context, _ domain.SeriesID, _ string) (*seriesdetail.Detail, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeRefresher struct {
	calledInstance domain.InstanceName
	calledSonarrID domain.SonarrSeriesID
	result         seriesrefresh.Result
	err            error
}

func (f *fakeRefresher) Refresh(_ context.Context, inst domain.InstanceName, sid domain.SonarrSeriesID) (seriesrefresh.Result, error) {
	f.calledInstance = inst
	f.calledSonarrID = sid
	if f.err != nil {
		return seriesrefresh.Result{}, f.err
	}
	return f.result, nil
}

// --- helpers ---

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func buildGlobalHandler(t *testing.T, cache seriesdetail.SeriesCacheLookupPort, composer seriesdetail.ComposerPort, tmdb seriesdetail.TMDBFallbackPort, refresher seriesdetailrest.SeriesRefresher) *seriesdetailrest.GlobalSeriesHandler {
	t.Helper()
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  cache,
		Composer:     composer,
		TMDBFallback: tmdb,
		Logger:       quietLogger(),
	})
	require.NoError(t, err)
	return seriesdetailrest.NewGlobalSeriesHandler(uc, cache, refresher, quietLogger())
}

// --- Get tests ---

func TestGlobalSeriesHandler_Get_InLibrary_OneInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{
		entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 140}},
	}
	composer := &fakeGlobalComposerDelegate{resp: &seriesdetail.Detail{
		Instance:       "homelab",
		SonarrSeriesID: 140,
		SeriesID:       140,
		Canon:          series.Canon{Title: "Rick and Morty"},
	}}
	tmdb := &fakeGlobalTMDBFallback{}
	h := buildGlobalHandler(t, cache, composer, tmdb, &fakeRefresher{})

	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body dto.SeriesDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []string{"homelab"}, body.InLibraryInstances)
	assert.Equal(t, "Rick and Morty", body.Hero.Title)
}

func TestGlobalSeriesHandler_Get_NotInLibrary_FallsBackToTMDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{entries: nil}
	composer := &fakeGlobalComposerDelegate{}
	tmdb := &fakeGlobalTMDBFallback{resp: &seriesdetail.Detail{
		SeriesID:           99999,
		Canon:              series.Canon{Title: "Discovered Show"},
		InLibraryInstances: []domain.InstanceName{},
	}}
	h := buildGlobalHandler(t, cache, composer, tmdb, &fakeRefresher{})

	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/99999", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body dto.SeriesDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, []string{}, body.InLibraryInstances)
	assert.NotEmpty(t, body.Hero.Title)
}

func TestGlobalSeriesHandler_Get_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, &fakeRefresher{})
	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id, nil)
		r.ServeHTTP(rec, req)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "id=%s", id)
	}
}

// --- Regrab tests ---

func TestGlobalSeriesHandler_Regrab_DispatchesToPreferredInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
	}}
	refresh := &fakeRefresher{result: seriesrefresh.Result{
		SeriesID: 140, SeriesQueued: true, Persons: 10, OMDbQueued: false,
	}}
	h := buildGlobalHandler(t, cache, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, refresh)

	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/140/regrab", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, domain.InstanceName("alpha"), refresh.calledInstance)
	assert.Equal(t, domain.SonarrSeriesID(99), refresh.calledSonarrID)
	var body dto.SeriesRefreshResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 10, body.Persons)
	assert.Equal(t, domain.SeriesID(140), body.SeriesID)
}

func TestGlobalSeriesHandler_Regrab_NotInLibrary_404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{entries: nil}
	h := buildGlobalHandler(t, cache, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, &fakeRefresher{})
	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/140/regrab", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGlobalSeriesHandler_Regrab_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, &fakeRefresher{})
	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/0/regrab", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGlobalSeriesHandler_Regrab_SingleInstance_DispatchesIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 140},
	}}
	refresh := &fakeRefresher{result: seriesrefresh.Result{SeriesID: 140, SeriesQueued: true}}
	h := buildGlobalHandler(t, cache, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, refresh)
	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/140/regrab", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, domain.InstanceName("homelab"), refresh.calledInstance)
}

// Use middleware so c.Error dispatches a typed 500 envelope.
func TestGlobalSeriesHandler_Get_CacheError_500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGlobalCacheLookup{err: errors.New("db down")} //nolint:err113
	h := buildGlobalHandler(t, cache, &fakeGlobalComposerDelegate{}, &fakeGlobalTMDBFallback{}, &fakeRefresher{})
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLogger()))
	r.GET("/api/v1/series/:id", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
