package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

func newDiscoverTestRig(t *testing.T, sonarrHandler http.HandlerFunc) (*gin.Engine, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(sonarrHandler)
	t.Cleanup(srv.Close)

	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewQbitDiscoverHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)
	return r, srv
}

func TestQbitDiscover_200MatchFirstEnabled(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"qb-disabled","implementation":"QBittorrent","enable":false,
			 "fields":[{"name":"host","value":"10.0.0.1"},{"name":"port","value":8080}]},
			{"id":2,"name":"qb-main","implementation":"QBittorrent","enable":true,
			 "fields":[{"name":"host","value":"10.0.0.2"},{"name":"port","value":8081},
				{"name":"username","value":"sonarr"},{"name":"tvCategory","value":"tv"}]}
		]`))
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "qb-main", got["name"])
	assert.Equal(t, "http://10.0.0.2:8081", got["url"])
	assert.Equal(t, "sonarr", got["username"])
	assert.Equal(t, "tv", got["category"])
}

func TestQbitDiscover_200FallbackWhenAllDisabled(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"qb-disabled","implementation":"qbittorrent","enable":false,
			 "fields":[{"name":"host","value":"10.0.0.1"},{"name":"port","value":8080}]}
		]`))
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "lowercase 'qbittorrent' matches; first wins when all disabled")
}

func TestQbitDiscover_404NoQbit(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"tr","implementation":"Transmission","enable":true,"fields":[]}
		]`))
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "NO_QBIT_FOUND", got["code"])
}

func TestQbitDiscover_404UnknownInstance(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/ghost/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: ghost")
}

func TestQbitDiscover_502SonarrUnauthorized(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unauthorized")
}

func TestQbitDiscover_502SonarrNetworkError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	client := sonarr.New("alpha", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewQbitDiscoverHandler(reg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/instances/alpha/discover/qbit", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unavailable")
}
