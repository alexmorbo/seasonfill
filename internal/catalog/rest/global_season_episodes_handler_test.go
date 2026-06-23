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
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 492 / N-1b — global season-episodes wrapper tests. Covers
// the wrapper's owned logic (400 / 404 / 500 + lex-first preference).
// The 200 upstream-fetch path delegates to InstancesHandler.SeasonEpisodes
// which has its own coverage in instances_season_episodes_test.go;
// full end-to-end validation lives in the live-curl smoke step.

type stubGlobalSeasonEpisodesCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalSeasonEpisodesCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

func quietLoggerSeasonEpisodesWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeasonEpisodesHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalSeasonEpisodesHandler(nil, &stubGlobalSeasonEpisodesCacheLookup{}, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/seasons/1/episodes", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

func TestGlobalSeasonEpisodesHandler_Get_400_InvalidSeason(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalSeasonEpisodesHandler(nil, &stubGlobalSeasonEpisodesCacheLookup{}, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	for _, s := range []string{"-1", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/seasons/"+s+"/episodes", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "season=%q", s)
	}
}

func TestGlobalSeasonEpisodesHandler_Get_404_NoInstances(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonEpisodesCacheLookup{entries: nil}
	h := catalogrest.NewGlobalSeasonEpisodesHandler(nil, cache, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/seasons/1/episodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGlobalSeasonEpisodesHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonEpisodesCacheLookup{err: errors.New("db down")} //nolint:err113
	h := catalogrest.NewGlobalSeasonEpisodesHandler(nil, cache, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerSeasonEpisodesWrapper()))
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/seasons/1/episodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeasonEpisodesHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonEpisodesCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := catalogrest.NewGlobalSeasonEpisodesHandler(nil, cache, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/seasons/1/episodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "season episodes handler not wired")
}

func TestGlobalSeasonEpisodesHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalSeasonEpisodesCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	// Non-nil inner — InstancesHandler with nil registry → SeasonEpisodes
	// will hit `inst.Client == nil` and emit 404. We don't assert on the
	// response code here; the post-splice c.Params capture is what
	// matters.
	innerHandler := catalogrest.NewInstancesHandler(nil, catalogrest.InstanceRegistry{}, quietLoggerSeasonEpisodesWrapper())
	h := catalogrest.NewGlobalSeasonEpisodesHandler(innerHandler, cache, quietLoggerSeasonEpisodesWrapper())
	r := gin.New()
	var capturedName, capturedID, capturedSeason string
	r.Use(func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				_ = rec
			}
			capturedName, _ = c.Params.Get("name")
			capturedID, _ = c.Params.Get("id")
			capturedSeason, _ = c.Params.Get("season")
		}()
		c.Next()
	})
	r.GET("/api/v1/series/:id/seasons/:season/episodes", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/seasons/3/episodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
	assert.Equal(t, "3", capturedSeason, ":season must be preserved untouched from the URL")
}
