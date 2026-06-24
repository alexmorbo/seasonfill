package rest_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 530 — global recommendations wrapper. Tests cover the wrapper's
// OWNED logic only. Story 532 — TMDB-fallback path.

type stubGlobalRecsCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalRecsCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

// stubRecommendationsFallback implements
// seriesdetailrest.TMDBFallbackRecommendationsPort.
type stubRecommendationsFallback struct {
	out *seriesdetail.Recommendations
	err error
}

func (s *stubRecommendationsFallback) GetRecommendations(_ context.Context, _ domain.SeriesID, _, _ int) (*seriesdetail.Recommendations, error) {
	return s.out, s.err
}

func quietLoggerRecsWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesRecommendationsHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, &stubGlobalRecsCacheLookup{}, nil, quietLoggerRecsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/recommendations", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

// TestGlobalSeriesRecommendationsHandler_Get_404_NoInstances_NilFallback —
// legacy behaviour: when no fallback UC is wired, the wrapper still
// returns the "series not in any library" 404. Renamed from
// ..._NoInstances after Story 532.
func TestGlobalSeriesRecommendationsHandler_Get_404_NoInstances_NilFallback(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, &stubGlobalRecsCacheLookup{}, nil, quietLoggerRecsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/recommendations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "series not in any library")
}

func TestGlobalSeriesRecommendationsHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalRecsCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, cache, nil, quietLoggerRecsWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerRecsWrapper()))
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/recommendations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesRecommendationsHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalRecsCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, cache, nil, quietLoggerRecsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/recommendations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "recommendations handler not wired")
}

func TestGlobalSeriesRecommendationsHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalRecsCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	innerHandler := seriesdetailrest.NewSeriesRecommendationsHandler(nil, quietLoggerRecsWrapper())
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(innerHandler, cache, nil, quietLoggerRecsWrapper())
	r := gin.New()
	var capturedName, capturedID string
	r.Use(func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				_ = rec
			}
			capturedName, _ = c.Params.Get("name")
			capturedID, _ = c.Params.Get("id")
		}()
		c.Next()
	})
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/recommendations?limit=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
}

// Story 532 — TMDB-fallback path returns 200 with canon-only payload
// when no library carries the series and a fallback UC is wired.
func TestGlobalSeriesRecommendationsHandler_Get_TMDBFallback_Returns200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalRecsCacheLookup{entries: nil}
	fallback := &stubRecommendationsFallback{out: &seriesdetail.Recommendations{
		SeriesID: 8378,
		Items:    []seriesdetail.RecommendationDetail{},
		Degraded: []string{"tmdb_series"},
	}}
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, cache, fallback, quietLoggerRecsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/8378/recommendations?limit=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"tmdb_series"`)
	assert.Contains(t, w.Body.String(), `"series_id":8378`)
}

// Story 532 — TMDB-fallback path returns 404 series_not_found when the
// fallback UC reports ports.ErrNotFound (truly unknown id).
func TestGlobalSeriesRecommendationsHandler_Get_TMDBFallback_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalRecsCacheLookup{entries: nil}
	fallback := &stubRecommendationsFallback{err: errors.Join(errors.New("canon load"), ports.ErrNotFound)} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesRecommendationsHandler(nil, cache, fallback, quietLoggerRecsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/recommendations?limit=20", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"series_not_found"`)
}
