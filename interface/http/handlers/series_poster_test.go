package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/config"
)

func newPosterTestRig(t *testing.T, h http.HandlerFunc) (*gin.Engine, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	handler := NewSeriesPosterHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/poster", handler.Proxy)
	return r, srv
}

func TestSeriesPoster_200StreamsImage(t *testing.T) {
	body := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10} // JPEG magic
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/api/v3/MediaCover/123/poster.jpg", req.URL.Path)
		assert.Equal(t, "k", req.Header.Get("X-Api-Key"))
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))
	assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))
	assert.Equal(t, `"abc"`, w.Header().Get("ETag"))
	assert.Equal(t, body, w.Body.Bytes())
}

func TestSeriesPoster_200SizeSmallHitsResizedVariant(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/api/v3/MediaCover/9/poster-500.jpg", req.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff})
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/9/poster?size=small", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestSeriesPoster_304WhenIfNoneMatchMatches(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		assert.Equal(t, `"abc"`, req.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusNotModified)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	req.Header.Set("If-None-Match", `"abc"`)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotModified, w.Code)
	assert.Equal(t, `"abc"`, w.Header().Get("ETag"))
	assert.Equal(t, "public, max-age=86400", w.Header().Get("Cache-Control"))
	assert.Empty(t, w.Body.Bytes())
}

func TestSeriesPoster_404FromUpstream(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "poster not found")
}

func TestSeriesPoster_404UnknownInstance(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, _ *http.Request) {})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/ghost/series/123/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: ghost")
}

func TestSeriesPoster_400InvalidID(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, _ *http.Request) {})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/abc/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid series id")
}

func TestSeriesPoster_502SonarrUnauthorized(t *testing.T) {
	r, _ := newPosterTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unauthorized")
}

func TestSeriesPoster_502SonarrNetworkError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := sonarr.New("alpha", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	handler := NewSeriesPosterHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/poster", handler.Proxy)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unavailable")
}
