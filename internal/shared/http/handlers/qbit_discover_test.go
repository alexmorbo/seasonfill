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
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

func newDiscoverTestRig(t *testing.T, sonarrHandler http.HandlerFunc) (*gin.Engine, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(sonarrHandler)
	t.Cleanup(srv.Close)

	client := sonarr.New("alpha", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := catalogrest.InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewQbitDiscoverHandler(reg, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)
	return r, srv
}

// fakeRadarrLookup satisfies catalogrest.RadarrConfigLookup.
type fakeRadarrLookup struct {
	m map[string]scan.RadarrInstance
}

func (f fakeRadarrLookup) Load() map[string]scan.RadarrInstance { return f.m }

// newRadarrDiscoverTestRig wires a handler whose SONARR registry holds only
// "alpha" and whose RADARR lookup holds only "rad" (a real *radarr.Client
// against httptest — the handler type-asserts to the concrete type, so a
// ports.RadarrClient mock cannot be used here). ADR-0023 F2.
func newRadarrDiscoverTestRig(t *testing.T, radarrHandler http.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	srv := httptest.NewServer(radarrHandler)
	t.Cleanup(srv.Close)

	client := radarr.New("rad", srv.URL, "k", 2*time.Second,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := catalogrest.InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}},
		}
	}}
	lookup := fakeRadarrLookup{m: map[string]scan.RadarrInstance{
		"rad": {
			Config: runtime.InstanceSnapshot{Name: "rad", Type: "radarr"},
			Client: client,
		},
	}}
	h := NewQbitDiscoverHandler(reg, lookup, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)
	return r
}

func discoverGet(t *testing.T, r *gin.Engine, name string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/instances/"+name+"/discover/qbit", nil)
	r.ServeHTTP(w, req)
	return w
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
	w := discoverGet(t, r, "alpha")
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
	w := discoverGet(t, r, "alpha")
	require.Equal(t, http.StatusOK, w.Code, "lowercase 'qbittorrent' matches; first wins when all disabled")
}

func TestQbitDiscover_404NoQbit(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"tr","implementation":"Transmission","enable":true,"fields":[]}
		]`))
	})
	w := discoverGet(t, r, "alpha")
	require.Equal(t, http.StatusNotFound, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "NO_QBIT_FOUND", got["code"])
	assert.Equal(t, "no qBittorrent download client configured in this instance", got["error"],
		"F2: the message must be arr-neutral — it is served for radarr too")
}

func TestQbitDiscover_404UnknownInstance(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {})
	w := discoverGet(t, r, "ghost")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: ghost")
}

func TestQbitDiscover_502SonarrUnauthorized(t *testing.T) {
	t.Parallel()
	r, _ := newDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	w := discoverGet(t, r, "alpha")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unauthorized")
}

func TestQbitDiscover_502SonarrNetworkError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	client := sonarr.New("alpha", "http://127.0.0.1:1", "k", 200*time.Millisecond,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	reg := catalogrest.InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{
			"alpha": {Config: config.SonarrInstance{Name: "alpha"}, Client: client},
		}
	}}
	h := NewQbitDiscoverHandler(reg, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)

	w := discoverGet(t, r, "alpha")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "sonarr unavailable")
}

// ---- ADR-0023 F2: radarr path ----

func TestQbitDiscover_Radarr_200(t *testing.T) {
	t.Parallel()
	r := newRadarrDiscoverTestRig(t, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "/api/v3/downloadclient", req.URL.Path)
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"qb-rad","implementation":"QBittorrent","enable":true,
			 "fields":[{"name":"host","value":"10.0.0.7"},{"name":"port","value":8080},
				{"name":"username","value":"radarr"},{"name":"movieCategory","value":"movies"}]}
		]`))
	})
	w := discoverGet(t, r, "rad")
	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "qb-rad", got["name"])
	assert.Equal(t, "http://10.0.0.7:8080", got["url"])
	assert.Equal(t, "radarr", got["username"])
	assert.Equal(t, "movies", got["category"])
}

func TestQbitDiscover_Radarr_404NoQbit(t *testing.T) {
	t.Parallel()
	r := newRadarrDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"tr","implementation":"Transmission","enable":true,"fields":[]}
		]`))
	})
	w := discoverGet(t, r, "rad")
	require.Equal(t, http.StatusNotFound, w.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "NO_QBIT_FOUND", got["code"])
	assert.Equal(t, "no qBittorrent download client configured in this instance", got["error"])
}

func TestQbitDiscover_Radarr_502Unauthorized(t *testing.T) {
	t.Parallel()
	r := newRadarrDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	w := discoverGet(t, r, "rad")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "radarr unauthorized")
}

// A sonarr name still resolves through the sonarr registry even when a radarr
// lookup is wired — the radarr branch must never shadow the sonarr one.
func TestQbitDiscover_Radarr_SonarrPathUnaffected(t *testing.T) {
	t.Parallel()
	r := newRadarrDiscoverTestRig(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	// "alpha" is in the sonarr registry with a nil Client → falls through to
	// the radarr lookup (miss) → unknown instance. Proves the sonarr branch is
	// entered first and does not leak into the radarr client.
	w := discoverGet(t, r, "alpha")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: alpha")
}

// nil radarrLookup must degrade gracefully (404), never panic — minimal/test
// wirings leave it unset.
func TestQbitDiscover_NilRadarrLookup_404NoPanic(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := NewQbitDiscoverHandler(catalogrest.InstanceRegistry{}, nil,
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/discover/qbit", h.Discover)

	w := discoverGet(t, r, "rad")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "unknown instance: rad")
}
