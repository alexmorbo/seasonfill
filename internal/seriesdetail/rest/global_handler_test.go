package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
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

func (f *fakeGlobalCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		out[id] = f.entries
	}
	return out, nil
}

// fakeSkeletonComposer satisfies seriesdetail.SkeletonComposerPort.
type fakeSkeletonComposer struct {
	resp seriesdetail.SkeletonDTO
	err  error
}

func (f *fakeSkeletonComposer) Compose(_ context.Context, _ domain.SeriesID, _ values.LanguageTag) (seriesdetail.SkeletonDTO, error) {
	if f.err != nil {
		return seriesdetail.SkeletonDTO{}, f.err
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

func buildGlobalHandler(t *testing.T, cache seriesdetail.SeriesCacheLookupPort, skeleton seriesdetail.SkeletonComposerPort, refresher seriesdetailrest.SeriesRefresher) *seriesdetailrest.GlobalSeriesHandler {
	t.Helper()
	uc, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		Skeleton: skeleton,
		Logger:   quietLogger(),
	})
	require.NoError(t, err)
	return seriesdetailrest.NewGlobalSeriesHandler(uc, cache, refresher, quietLogger())
}

func skeletonWithTitle(t *testing.T, seriesID domain.SeriesID, title string, instances []string) seriesdetail.SkeletonDTO {
	t.Helper()
	tag, err := values.NewLanguageTag("en-US")
	require.NoError(t, err)
	vt, err := values.NewTitle(title, tag)
	require.NoError(t, err)
	dtoOut := seriesdetail.SkeletonDTO{
		SeriesID:           seriesID,
		Lang:               tag,
		SeasonCount:        3,
		InLibraryInstances: instances,
	}
	dtoOut.Hero.Title = vt
	return dtoOut
}

// --- Get tests ---

func TestGlobalSeriesHandler_Get_InLibrary_SkeletonShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	skeleton := &fakeSkeletonComposer{resp: skeletonWithTitle(t, 140, "Rick and Morty", []string{"homelab"})}
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, skeleton, &fakeRefresher{})

	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &keys))
	// Skeleton keys present.
	for _, k := range []string{"series_id", "hero", "sidebar", "season_count", "in_library_instances"} {
		assert.Containsf(t, keys, k, "skeleton must carry %q", k)
	}
	// Fat per-instance keys absent.
	for _, k := range []string{"library", "seasons", "cast", "recommendations", "download", "torrents"} {
		assert.NotContainsf(t, keys, k, "skeleton must NOT carry %q", k)
	}

	assert.JSONEq(t, `["homelab"]`, string(keys["in_library_instances"]))

	var hero map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(keys["hero"], &hero))
	assert.Contains(t, string(hero["title"]), "Rick and Morty")
}

func TestGlobalSeriesHandler_Get_TMDBOnly_EmptyInstances(t *testing.T) {
	gin.SetMode(gin.TestMode)
	skeleton := &fakeSkeletonComposer{resp: skeletonWithTitle(t, 99999, "Discovered Show", []string{})}
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, skeleton, &fakeRefresher{})

	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/99999", nil)
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &keys))
	assert.JSONEq(t, `[]`, string(keys["in_library_instances"]))
	assert.JSONEq(t, `99999`, string(keys["series_id"]))
	assert.Contains(t, keys, "hero")
}

func TestGlobalSeriesHandler_Get_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, &fakeSkeletonComposer{}, &fakeRefresher{})
	r := gin.New()
	r.GET("/api/v1/series/:id", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id, nil)
		r.ServeHTTP(rec, req)
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "id=%s", id)
	}
}

// ErrNotFound from the skeleton maps to 404 via the error middleware.
func TestGlobalSeriesHandler_Get_NotFound_404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	skeleton := &fakeSkeletonComposer{err: fmt.Errorf("skeleton canon load: %w", ports.ErrNotFound)}
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, skeleton, &fakeRefresher{})
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLogger()))
	r.GET("/api/v1/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// A generic compose error dispatches a typed 500 envelope.
func TestGlobalSeriesHandler_Get_ComposeError_500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	skeleton := &fakeSkeletonComposer{err: errors.New("db down")} //nolint:err113
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, skeleton, &fakeRefresher{})
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLogger()))
	r.GET("/api/v1/series/:id", h.Get)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
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
	h := buildGlobalHandler(t, cache, &fakeSkeletonComposer{}, refresh)

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
	h := buildGlobalHandler(t, cache, &fakeSkeletonComposer{}, &fakeRefresher{})
	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/140/regrab", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGlobalSeriesHandler_Regrab_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := buildGlobalHandler(t, &fakeGlobalCacheLookup{}, &fakeSkeletonComposer{}, &fakeRefresher{})
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
	h := buildGlobalHandler(t, cache, &fakeSkeletonComposer{}, refresh)
	r := gin.New()
	r.POST("/api/v1/series/:id/regrab", h.Regrab)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/series/140/regrab", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, domain.InstanceName("homelab"), refresh.calledInstance)
}
