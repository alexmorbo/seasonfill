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

// Reuses fakeCachePort / fakeSeriesPort / emptyKwRefs / fakeNoTexts +
// newComposerForHandlerTest from series_detail_test.go (same package).

func TestSeriesOverviewHandler_Get_200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	awards := "Won 16 Emmys"
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42, OriginalTitle: new("Breaking Bad"), OMDBAwards: &awards},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "Breaking Bad"},
		},
	)
	h := NewSeriesOverviewHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/overview?lang=ru-RU", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.SeriesOverviewResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, domain.InstanceName("alpha"), body.Instance)
	require.Equal(t, domain.SonarrSeriesID(1), body.SonarrSeriesID)
	require.Equal(t, domain.SeriesID(42), body.SeriesID)
	require.Equal(t, "ru-RU", body.Lang)
	require.NotNil(t, body.Overview.Awards)
	require.Equal(t, "Won 16 Emmys", *body.Overview.Awards)
	// Keywords slice always present (never nil — even when empty).
	require.NotNil(t, body.Overview.Keywords)
	require.NotNil(t, body.Degraded)
}

func TestSeriesOverviewHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{},
	)
	h := NewSeriesOverviewHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/overview", h.Get)

	for _, id := range []string{"0", "-3", "xyz"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/"+id+"/overview", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "id=%q", id)
	}
}

func TestSeriesOverviewHandler_Get_404_PropagatesComposerError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{}, // no cache row → composer returns ports.ErrNotFound
	)
	h := NewSeriesOverviewHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.GET("/api/v1/instances/:name/series/:id/overview", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999/overview", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	// ErrorResponseMiddleware maps ports.ErrNotFound → 404.
	require.Equal(t, http.StatusNotFound, rec.Code)
}
