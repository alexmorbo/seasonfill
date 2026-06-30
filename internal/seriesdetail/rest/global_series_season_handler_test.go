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
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 492 / N-1b — global season wrapper tests. Mirror the cast
// wrapper coverage (400 / 404 / 500 + lex-first preference) plus an
// invalid-season case.

type stubGlobalSeasonCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalSeasonCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

func (s *stubGlobalSeasonCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		out[id] = s.entries
	}
	return out, nil
}

func quietLoggerSeasonWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesSeasonHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(nil, &stubGlobalSeasonCacheLookup{}, quietLoggerSeasonWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/season/1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

func TestGlobalSeriesSeasonHandler_Get_400_InvalidSeason(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(nil, &stubGlobalSeasonCacheLookup{}, quietLoggerSeasonWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	for _, n := range []string{"-1", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/season/"+n, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "n=%q", n)
	}
}

func TestGlobalSeriesSeasonHandler_Get_404_NoInstances(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonCacheLookup{entries: nil}
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(nil, cache, quietLoggerSeasonWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/season/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGlobalSeriesSeasonHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(nil, cache, quietLoggerSeasonWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerSeasonWrapper()))
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/season/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesSeasonHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(nil, cache, quietLoggerSeasonWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/season/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "season handler not wired")
}

func TestGlobalSeriesSeasonHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	innerHandler := seriesdetailrest.NewSeriesSeasonHandler(nil, quietLoggerSeasonWrapper())
	h := seriesdetailrest.NewGlobalSeriesSeasonHandler(innerHandler, cache, quietLoggerSeasonWrapper())
	r := gin.New()
	var capturedName, capturedID, capturedN string
	r.Use(func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				_ = rec
			}
			capturedName, _ = c.Params.Get("name")
			capturedID, _ = c.Params.Get("id")
			capturedN, _ = c.Params.Get("n")
		}()
		c.Next()
	})
	r.GET("/api/v1/series/:id/season/:n", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/season/3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
	assert.Equal(t, "3", capturedN, ":n must be preserved untouched from the URL")
}
