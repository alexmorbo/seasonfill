package rest_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Story 577 / E-1-B2 — global library handler tests. Mirrors
// global_series_torrents_handler_test.go. Covers the wrapper's owned logic
// (400 / 204 / 404 / 500 + lex-first default + explicit instance + body shape).

type stubLibCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (s *stubLibCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return s.entries, s.err
}

func (s *stubLibCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		out[id] = s.entries
	}
	return out, s.err
}

type stubLibComposer struct {
	view seriesdetail.LibraryView
	err  error
	got  domain.InstanceName
}

func (s *stubLibComposer) Compose(_ context.Context, _ domain.SeriesID, instanceName domain.InstanceName) (seriesdetail.LibraryView, error) {
	s.got = instanceName
	return s.view, s.err
}

func quietLoggerLibrary() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGlobalSeriesLibraryHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(&stubLibComposer{}, &stubLibCacheLookup{}, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	for _, id := range []string{"0", "-5", "abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/library", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
	}
}

func TestGlobalSeriesLibraryHandler_Get_204_NoInstances(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(&stubLibComposer{}, &stubLibCacheLookup{entries: nil}, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/library", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestGlobalSeriesLibraryHandler_Get_404_UnknownInstance(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 7}}}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(&stubLibComposer{}, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library?instance=beta", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "instance_not_found")
}

func TestGlobalSeriesLibraryHandler_Get_404_ComposerNotInInstance(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 7}}}
	composer := &stubLibComposer{err: seriesdetail.ErrSeriesNotInInstance}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(composer, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library?instance=homelab", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "instance_not_found")
}

func TestGlobalSeriesLibraryHandler_Get_500_CacheError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{err: errors.New("db down")} //nolint:err113
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(&stubLibComposer{}, cache, quietLoggerLibrary())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(quietLoggerLibrary()))
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGlobalSeriesLibraryHandler_Get_500_NilComposer(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 7}}}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(nil, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "library composer not wired")
}

func TestGlobalSeriesLibraryHandler_Get_200_LexFirstDefault(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "gamma", SonarrSeriesID: 1},
		{InstanceName: "alpha", SonarrSeriesID: 2},
		{InstanceName: "beta", SonarrSeriesID: 3},
	}}
	composer := &stubLibComposer{view: seriesdetail.LibraryView{
		Instance: "alpha", SonarrSeriesID: 2, SeriesID: 42, Monitored: true,
		SyncedAt: time.Now().UTC(),
	}}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(composer, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, domain.InstanceName("alpha"), composer.got)

	var body dto.SeriesLibraryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.Monitored)
	assert.NotNil(t, body.Recent)
	assert.False(t, body.SyncedAt.IsZero())
}

func TestGlobalSeriesLibraryHandler_Get_200_ExplicitInstance(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{
		{InstanceName: "alpha", SonarrSeriesID: 1},
		{InstanceName: "beta", SonarrSeriesID: 2},
	}}
	composer := &stubLibComposer{view: seriesdetail.LibraryView{Instance: "beta", SonarrSeriesID: 2, SeriesID: 42}}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(composer, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library?instance=beta", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, domain.InstanceName("beta"), composer.got)
}

func TestGlobalSeriesLibraryHandler_Get_200_Body_NextEpisodeAndInProgress(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	air := time.Now().UTC().Add(48 * time.Hour)
	cache := &stubLibCacheLookup{entries: []series.CacheEntry{{InstanceName: "homelab", SonarrSeriesID: 7}}}
	composer := &stubLibComposer{view: seriesdetail.LibraryView{
		Instance: "homelab", SonarrSeriesID: 7, SeriesID: 42,
		InProgress:       &seriesdetail.InProgressDetail{SeasonNumber: 5, EpisodeNumber: 3, Percent: 45},
		NextEpisodeToAir: &seriesdetail.NextEpisodeDetail{SeasonNumber: 6, EpisodeNumber: 1, AirDate: &air},
	}}
	h := seriesdetailrest.NewGlobalSeriesLibraryHandler(composer, cache, quietLoggerLibrary())
	r := gin.New()
	r.GET("/api/v1/series/:id/library", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/library?instance=homelab", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.Contains(t, body, "in_progress")
	assert.Contains(t, body, "next_episode_to_air")

	var resp dto.SeriesLibraryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.InProgress)
	require.NotNil(t, resp.Library.InProgress, "InProgress mirrored under library.in_progress")
	require.NotNil(t, resp.NextEpisodeToAir)
	assert.Equal(t, 45, resp.InProgress.Percent)
}
