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

// Story 529 — global overview wrapper. Tests cover the wrapper's
// OWNED logic only (400 / 404 / 500 paths + lex-first preference for
// the spliced :name + :id). The delegation body lives on the inner
// per-instance SeriesOverviewHandler which has its own test coverage.
//
// Story 532 — TMDB-fallback path: when no instance carries the series
// and a fallback UC is wired, the wrapper returns 200 with a canon-only
// payload (degraded=["tmdb_series"]); the fallback's ports.ErrNotFound
// becomes 404 with body "series_not_found".

type stubGlobalOverviewCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalOverviewCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

// stubOverviewFallback implements seriesdetailrest.TMDBFallbackOverviewPort.
type stubOverviewFallback struct {
	out *seriesdetail.Overview
	err error
}

func (s *stubOverviewFallback) GetOverview(_ context.Context, _ domain.SeriesID, _ string) (*seriesdetail.Overview, error) {
	return s.out, s.err
}

func quietLoggerOverviewWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesOverviewHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, &stubGlobalOverviewCacheLookup{}, nil, quietLoggerOverviewWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/overview", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/overview", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

// TestGlobalSeriesOverviewHandler_Get_404_NoInstances_NilFallback —
// legacy behaviour: when no fallback UC is wired, the wrapper still
// returns the "series not in any library" 404. Renamed from
// ..._NoInstances after Story 532.
func TestGlobalSeriesOverviewHandler_Get_404_NoInstances_NilFallback(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{entries: nil}
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, cache, nil, quietLoggerOverviewWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "series not in any library")
}

func TestGlobalSeriesOverviewHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, cache, nil, quietLoggerOverviewWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerOverviewWrapper()))
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesOverviewHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, cache, nil, quietLoggerOverviewWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "overview handler not wired")
}

// TestGlobalSeriesOverviewHandler_Get_LexFirstPreference asserts the wrapper
// picks the lex-first instance and replaces :id with the matching
// per-instance sonarr_series_id BEFORE delegating to the inner. Mirrors
// the GlobalSeriesCastHandler lex-first test: inner with a nil composer
// panics in Get; recovery middleware captures c.Params post-splice.
func TestGlobalSeriesOverviewHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	innerHandler := seriesdetailrest.NewSeriesOverviewHandler(nil, quietLoggerOverviewWrapper())
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(innerHandler, cache, nil, quietLoggerOverviewWrapper())
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
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/overview?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
}

// Story 532 — TMDB-fallback path returns 200 with canon-only payload
// when no library carries the series and a fallback UC is wired.
func TestGlobalSeriesOverviewHandler_Get_TMDBFallback_Returns200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{entries: nil}
	fallback := &stubOverviewFallback{out: &seriesdetail.Overview{
		SeriesID:    8378,
		Lang:        "ru-RU",
		Description: "Краткое описание",
		Degraded:    []string{"tmdb_series"},
	}}
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, cache, fallback, quietLoggerOverviewWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/8378/overview?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"tmdb_series"`)
	assert.Contains(t, w.Body.String(), `"series_id":8378`)
}

// Story 532 — TMDB-fallback path returns 404 series_not_found when the
// fallback UC reports ports.ErrNotFound (truly unknown id).
func TestGlobalSeriesOverviewHandler_Get_TMDBFallback_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalOverviewCacheLookup{entries: nil}
	fallback := &stubOverviewFallback{err: errors.Join(errors.New("canon load"), ports.ErrNotFound)} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesOverviewHandler(nil, cache, fallback, quietLoggerOverviewWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"series_not_found"`)
}
