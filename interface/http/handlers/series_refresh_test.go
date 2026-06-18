package handlers

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

	"github.com/alexmorbo/seasonfill/application/enrichment"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type refreshHandlerFakeCache struct {
	entry series.CacheEntry
	err   error
}

func (f *refreshHandlerFakeCache) Get(_ context.Context, _ string, _ int) (series.CacheEntry, error) {
	return f.entry, f.err
}
func (f *refreshHandlerFakeCache) Upsert(_ context.Context, _ series.CacheEntry) error { return nil }
func (f *refreshHandlerFakeCache) SoftDelete(_ context.Context, _ string, _ int) error { return nil }
func (f *refreshHandlerFakeCache) ListActiveByInstance(_ context.Context, _ string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *refreshHandlerFakeCache) ListByFilter(_ context.Context, _ string, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *refreshHandlerFakeCache) FetchLastGrabInfo(_ context.Context, _ string, _ []int) (map[int]ports.LastGrabInfo, error) {
	return make(map[int]ports.LastGrabInfo), nil
}
func (f *refreshHandlerFakeCache) ListDistinctNetworks(_ context.Context, _ string) ([]string, error) {
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
func ptrString(v string) *string        { return &v }

func mustNewRefreshHandler(t *testing.T, uc *seriesrefresh.UseCase) *SeriesRefreshHandler {
	t.Helper()
	return NewSeriesRefreshHandler(uc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestSeriesRefreshHandler_202_Accepted(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &refreshHandlerFakeCache{entry: series.CacheEntry{SeriesID: ptrInt64(42)}}
	canon := &refreshHandlerFakeSeries{canon: seriesrefresh.CanonView{ID: 42, IMDBID: ptrString("tt9")}}
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

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/instances/alpha/series/7/refresh", nil)
		r.ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code)
	}
}
