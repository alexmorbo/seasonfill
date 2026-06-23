package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichment "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type refreshHandlerFakeCache struct {
	entry series.CacheEntry
	err   error
}

func (f *refreshHandlerFakeCache) Get(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) (series.CacheEntry, error) {
	return f.entry, f.err
}
func (f *refreshHandlerFakeCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (f *refreshHandlerFakeCache) SoftDelete(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID) error {
	return nil
}
func (f *refreshHandlerFakeCache) ListActiveByInstance(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *refreshHandlerFakeCache) ListByFilter(_ context.Context, _ domain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *refreshHandlerFakeCache) FetchLastGrabInfo(_ context.Context, _ domain.InstanceName, _ []domain.SonarrSeriesID) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[domain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (f *refreshHandlerFakeCache) ListDistinctNetworks(_ context.Context, _ domain.InstanceName) ([]string, error) {
	return nil, nil
}
func (f *refreshHandlerFakeCache) GetInstancesBySeriesID(_ context.Context, _ domain.SeriesID) ([]domain.InstanceName, error) {
	return nil, nil
}
func (f *refreshHandlerFakeCache) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return nil, nil
}

type refreshHandlerFakeSeries struct{ canon seriesrefresh.CanonView }

func (f *refreshHandlerFakeSeries) Get(_ context.Context, _ domain.SeriesID) (seriesrefresh.CanonView, error) {
	return f.canon, nil
}

type refreshHandlerFakeCast struct{ ids []int64 }

func (f *refreshHandlerFakeCast) TopCastPersonIDs(_ context.Context, _ domain.SeriesID, _ int) ([]int64, error) {
	return f.ids, nil
}

type refreshHandlerFakeDispatcher struct{ count int }

func (d *refreshHandlerFakeDispatcher) Enqueue(_ enrichment.EntityKind, _ int64, _ enrichment.Priority) {
	d.count++
}
func (d *refreshHandlerFakeDispatcher) Close() {}

func ptrInt64(v int64) *domain.SeriesID { sid := domain.SeriesID(v); return &sid }
func ptrIMDBID(v string) *domain.IMDBID { id := domain.IMDBID(v); return &id }

func mustNewRefreshHandler(t *testing.T, uc *seriesrefresh.UseCase) *SeriesRefreshHandler {
	t.Helper()
	return NewSeriesRefreshHandler(uc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSeriesRefreshHandler_202_Accepted(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &refreshHandlerFakeCache{entry: series.CacheEntry{SeriesID: ptrInt64(42)}}
	canon := &refreshHandlerFakeSeries{canon: seriesrefresh.CanonView{ID: 42, IMDBID: ptrIMDBID("tt9")}}
	cast := &refreshHandlerFakeCast{ids: []int64{1, 2}}
	disp := &refreshHandlerFakeDispatcher{}
	uc, err := seriesrefresh.New(seriesrefresh.Deps{
		SeriesCache:  cache,
		Series:       canon,
		SeriesPeople: cast,
		Dispatcher:   disp,
	})
	require.NoError(t, err)

	h := mustNewRefreshHandler(t, uc)
	r := gin.New()
	r.POST("/api/v1/instances/:name/series/:id/refresh", h.Refresh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/instances/alpha/series/7/refresh", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	var body dto.SeriesRefreshResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, domain.SeriesID(42), body.SeriesID)
	assert.True(t, body.SeriesQueued)
	assert.Equal(t, 2, body.Persons)
	assert.True(t, body.OMDbQueued)
	assert.Equal(t, 4, disp.count)
}

func TestSeriesRefreshHandler_400_BadID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &refreshHandlerFakeCache{}
	canon := &refreshHandlerFakeSeries{}
	disp := &refreshHandlerFakeDispatcher{}
	uc, err := seriesrefresh.New(seriesrefresh.Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)

	h := mustNewRefreshHandler(t, uc)
	r := gin.New()
	r.POST("/api/v1/instances/:name/series/:id/refresh", h.Refresh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/instances/alpha/series/abc/refresh", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSeriesRefreshHandler_404_NotFound(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &refreshHandlerFakeCache{err: ports.ErrNotFound}
	canon := &refreshHandlerFakeSeries{}
	disp := &refreshHandlerFakeDispatcher{}
	uc, err := seriesrefresh.New(seriesrefresh.Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)

	h := mustNewRefreshHandler(t, uc)
	r := gin.New()
	// F-2c-1: middleware so c.Error → JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.POST("/api/v1/instances/:name/series/:id/refresh", h.Refresh)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/instances/alpha/series/99/refresh", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSeriesRefreshHandler_Idempotent_Repeat(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &refreshHandlerFakeCache{entry: series.CacheEntry{SeriesID: ptrInt64(42)}}
	canon := &refreshHandlerFakeSeries{canon: seriesrefresh.CanonView{ID: 42}}
	disp := &refreshHandlerFakeDispatcher{}
	uc, err := seriesrefresh.New(seriesrefresh.Deps{SeriesCache: cache, Series: canon, Dispatcher: disp})
	require.NoError(t, err)

	h := mustNewRefreshHandler(t, uc)
	r := gin.New()
	r.POST("/api/v1/instances/:name/series/:id/refresh", h.Refresh)

	for range 3 {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/instances/alpha/series/7/refresh", nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code)
	}
}
