package rest

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Reuses newComposerForHandlerTest + i64p from series_detail_test.go
// (same package).

func TestSeriesRecommendationsHandler_Get_200_Empty(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42, Title: "Source"},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "Source"},
		},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.SeriesRecommendationsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, domain.InstanceName("alpha"), body.Instance)
	require.Equal(t, domain.SonarrSeriesID(1), body.SonarrSeriesID)
	require.Equal(t, 20, body.Limit, "default limit 20")
	require.Equal(t, 0, body.Offset)
	require.False(t, body.HasMore)
	require.NotNil(t, body.Items, "items slice must never be nil")
	require.Equal(t, 0, len(body.Items))
	require.NotNil(t, body.Degraded)
}

func TestSeriesRecommendationsHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(series.Canon{ID: 42}, map[string]series.CacheEntry{})
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, id := range []string{"0", "-3", "xyz"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/"+id+"/recommendations", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "id=%q", id)
	}
}

func TestSeriesRecommendationsHandler_Get_400_InvalidLimit(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)}},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, q := range []string{"limit=0", "limit=-1", "limit=51", "limit=abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations?"+q, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "q=%q", q)
	}
}

func TestSeriesRecommendationsHandler_Get_400_InvalidOffset(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)}},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, q := range []string{"offset=-1", "offset=abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations?"+q, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "q=%q", q)
	}
}

func TestSeriesRecommendationsHandler_Get_404_PropagatesComposerError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{}, // no cache row → composer returns ports.ErrNotFound
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999/recommendations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
