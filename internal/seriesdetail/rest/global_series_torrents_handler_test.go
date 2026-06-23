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

// Story 492 / N-1b — global torrents wrapper tests. Covers the
// wrapper's owned logic (400 / 404 / 500 + lex-first preference). The
// 200 happy + 304 If-None-Match paths execute on the inner
// SeriesTorrentsHandler which has its own coverage in
// series_torrents_test.go; full end-to-end validation lives in the
// live-curl smoke step.

type stubGlobalTorrentsCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubGlobalTorrentsCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}

func quietLoggerTorrentsWrapper() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesTorrentsHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesTorrentsHandler(nil, &stubGlobalTorrentsCacheLookup{}, quietLoggerTorrentsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/torrents", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/torrents", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

func TestGlobalSeriesTorrentsHandler_Get_404_NoInstances(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalTorrentsCacheLookup{entries: nil}
	h := seriesdetailrest.NewGlobalSeriesTorrentsHandler(nil, cache, quietLoggerTorrentsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/torrents", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/torrents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGlobalSeriesTorrentsHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalTorrentsCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesTorrentsHandler(nil, cache, quietLoggerTorrentsWrapper())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerTorrentsWrapper()))
	r.GET("/api/v1/series/:id/torrents", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/torrents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesTorrentsHandler_Get_500_NilInner(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalTorrentsCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "homelab", SonarrSeriesID: 7},
	}}
	h := seriesdetailrest.NewGlobalSeriesTorrentsHandler(nil, cache, quietLoggerTorrentsWrapper())
	r := gin.New()
	r.GET("/api/v1/series/:id/torrents", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/torrents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "torrents handler not wired")
}

func TestGlobalSeriesTorrentsHandler_Get_LexFirstPreference(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubGlobalTorrentsCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "beta", SonarrSeriesID: 7},
		{InstanceName: "alpha", SonarrSeriesID: 99},
		{InstanceName: "gamma", SonarrSeriesID: 11},
	}}
	// Non-nil inner with nil deps — the wrapper splices c.Params,
	// then the inner panics; recovery middleware captures the
	// post-splice Params for assertion.
	innerHandler := seriesdetailrest.NewSeriesTorrentsHandler(nil, nil, nil, quietLoggerTorrentsWrapper())
	h := seriesdetailrest.NewGlobalSeriesTorrentsHandler(innerHandler, cache, quietLoggerTorrentsWrapper())
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
	r.GET("/api/v1/series/:id/torrents", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/140/torrents", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "alpha", capturedName, "lex-first instance name must be spliced into :name")
	assert.Equal(t, "99", capturedID, "lex-first instance's per-instance sonarr_series_id must replace :id")
}
