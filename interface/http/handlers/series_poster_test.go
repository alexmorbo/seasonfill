package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// newPosterTestRigWithCache wires a cache into the handler so tests
// can exercise hit / miss / etag semantics.
func newPosterTestRigWithCache(t *testing.T, h http.HandlerFunc, cache sonarr.PosterCache) (*gin.Engine, *httptest.Server, *atomic.Int32) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var upstreamCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstreamCalls.Add(1)
		h(w, req)
	}))
	t.Cleanup(srv.Close)
	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	handler := NewSeriesPosterHandler(reg,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		WithPosterCache(cache))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/poster", handler.Proxy)
	return r, srv, &upstreamCalls
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

func TestSeriesPoster_304WhenIfNoneMatchMatches_NoCache(t *testing.T) {
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

// --- cache integration tests ---

func TestSeriesPoster_CacheHitOnSecondCallSkipsUpstream(t *testing.T) {
	body := []byte{0xff, 0xd8, 0xff, 0xe0}
	cache := sonarr.NewLRUPosterCache(1<<20, time.Hour)
	r, _, calls := newPosterTestRigWithCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}, cache)

	// First call — cache miss, upstream hit.
	w1 := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w1, req)
	require.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, body, w1.Body.Bytes())
	firstETag := w1.Header().Get("ETag")
	assert.NotEmpty(t, firstETag)
	assert.Equal(t, int32(1), calls.Load())

	// Second call — cache hit, NO upstream traffic.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, body, w2.Body.Bytes())
	assert.Equal(t, firstETag, w2.Header().Get("ETag"))
	assert.Equal(t, int32(1), calls.Load(), "second call must NOT hit upstream")
}

func TestSeriesPoster_304WhenIfNoneMatchMatchesSynthesizedETag(t *testing.T) {
	body := []byte{0xff}
	cache := sonarr.NewLRUPosterCache(1<<20, time.Hour)
	r, _, calls := newPosterTestRigWithCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}, cache)

	// Warm cache + capture synth ETag.
	w1 := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w1, req)
	etag := w1.Header().Get("ETag")
	require.NotEmpty(t, etag)
	require.Equal(t, int32(1), calls.Load())

	// Re-request with If-None-Match matching the synth ETag.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	req2.Header.Set("If-None-Match", etag)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusNotModified, w2.Code)
	assert.Equal(t, etag, w2.Header().Get("ETag"))
	assert.Equal(t, "public, max-age=86400", w2.Header().Get("Cache-Control"))
	assert.Empty(t, w2.Body.Bytes())
	assert.Equal(t, int32(1), calls.Load(), "304 fast path must NOT hit upstream")
}

func TestSeriesPoster_CacheKeyDifferentiatesSize(t *testing.T) {
	cache := sonarr.NewLRUPosterCache(1<<20, time.Hour)
	r, _, calls := newPosterTestRigWithCache(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(req.URL.Path))
	}, cache)

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster", nil)
	r.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/alpha/series/123/poster?size=small", nil)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	assert.Equal(t, int32(2), calls.Load(), "size variant must be a separate cache key")
	assert.NotEqual(t, w1.Body.Bytes(), w2.Body.Bytes())
}
