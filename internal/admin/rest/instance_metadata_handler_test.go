package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admininfra "github.com/alexmorbo/seasonfill/internal/admin/infrastructure"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

type fakeLookup struct {
	id     int64
	client ports.SonarrClient
}

func (f fakeLookup) Lookup(name string) (int64, ports.SonarrClient, bool) {
	if name != "main" {
		return 0, nil, false
	}
	return f.id, f.client, true
}

type mdSonarrClient struct {
	*ports.SonarrClientMock
	qpCalls atomic.Int32
	rfCalls atomic.Int32

	qpItems []ports.QualityProfile
	rfItems []ports.RootFolder
	qpErr   error
	rfErr   error
}

func newMDSonarr() *mdSonarrClient {
	cli := &mdSonarrClient{}
	cli.SonarrClientMock = &ports.SonarrClientMock{
		ListQualityProfilesFunc: func(_ context.Context) ([]ports.QualityProfile, error) {
			cli.qpCalls.Add(1)
			return cli.qpItems, cli.qpErr
		},
		ListRootFoldersFunc: func(_ context.Context) ([]ports.RootFolder, error) {
			cli.rfCalls.Add(1)
			return cli.rfItems, cli.rfErr
		},
	}
	return cli
}

func buildMetadataRouter(t *testing.T, cli *mdSonarrClient) *gin.Engine {
	t.Helper()
	cache := admininfra.NewMetadataCache("_h_" + t.Name())
	t.Cleanup(func() { _ = cache.Close() })
	uc := authapp.NewInstanceMetadataUseCase(fakeLookup{id: 100, client: cli}, cache, nil)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := NewInstanceMetadataHandler(uc, logger)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(logger))
	r.GET("/api/v1/instances/:name/quality-profiles", handler.GetQualityProfiles)
	r.GET("/api/v1/instances/:name/root-folders", handler.GetRootFolders)
	r.POST("/api/v1/instances/:name/refresh-metadata", handler.RefreshMetadata)
	return r
}

func httpDo(t *testing.T, r *gin.Engine, method, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func TestHandler_QualityProfiles_MissThenHit(t *testing.T) {
	t.Parallel()
	cli := newMDSonarr()
	cli.qpItems = []ports.QualityProfile{{ID: 1, Name: "Any"}, {ID: 2, Name: "HD-1080p"}}
	r := buildMetadataRouter(t, cli)

	w, body := httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "miss", body["cache_status"])
	assert.Equal(t, "main", body["instance_name"])
	assert.Len(t, body["items"], 2)

	_, body = httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	assert.Equal(t, "hit", body["cache_status"])
	assert.Equal(t, int32(1), cli.qpCalls.Load(),
		"second call MUST be served from cache")
}

func TestHandler_RootFolders_MissThenHit(t *testing.T) {
	t.Parallel()
	cli := newMDSonarr()
	cli.rfItems = []ports.RootFolder{{ID: 1, Path: "/tv", Accessible: true, FreeSpace: 4096}}
	r := buildMetadataRouter(t, cli)

	w, body := httpDo(t, r, http.MethodGet, "/api/v1/instances/main/root-folders")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "miss", body["cache_status"])
	first := body["items"].([]any)[0].(map[string]any)
	assert.Equal(t, "/tv", first["path"])
	assert.Equal(t, true, first["accessible"])
	assert.Equal(t, float64(4096), first["free_space"])

	_, body = httpDo(t, r, http.MethodGet, "/api/v1/instances/main/root-folders")
	assert.Equal(t, "hit", body["cache_status"])
	assert.Equal(t, int32(1), cli.rfCalls.Load())
}

func TestHandler_RefreshMetadata_Invalidates(t *testing.T) {
	t.Parallel()
	cli := newMDSonarr()
	cli.qpItems = []ports.QualityProfile{{ID: 1, Name: "Any"}}
	r := buildMetadataRouter(t, cli)

	httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	require.Equal(t, int32(1), cli.qpCalls.Load())

	w, body := httpDo(t, r, http.MethodPost, "/api/v1/instances/main/refresh-metadata")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, true, body["invalidated"])

	httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	assert.Equal(t, int32(2), cli.qpCalls.Load(),
		"refresh-metadata MUST evict so the next GET re-fetches")
}

func TestHandler_InstanceNotFound_404(t *testing.T) {
	t.Parallel()
	r := buildMetadataRouter(t, newMDSonarr())

	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/instances/ghost/quality-profiles"},
		{http.MethodGet, "/api/v1/instances/ghost/root-folders"},
		{http.MethodPost, "/api/v1/instances/ghost/refresh-metadata"},
	} {
		w, body := httpDo(t, r, tc.method, tc.path)
		require.Equal(t, http.StatusNotFound, w.Code, tc.path)
		assert.Equal(t, "instance_not_found", body["error"], tc.path)
	}
}

func TestHandler_SonarrUnreachable_502(t *testing.T) {
	t.Parallel()
	cli := newMDSonarr()
	cli.qpErr = errors.New("dial tcp: connection refused")
	r := buildMetadataRouter(t, cli)

	w, body := httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "sonarr_unreachable", body["error"])
	assert.Contains(t, body["message"], "main")
}

// Graceful degradation: cache hit + Sonarr now broken → 200 cached.
func TestHandler_SonarrUnreachable_GracefulFromCache(t *testing.T) {
	t.Parallel()
	cli := newMDSonarr()
	cli.qpItems = []ports.QualityProfile{{ID: 1, Name: "Any"}}
	r := buildMetadataRouter(t, cli)

	// Warm the cache.
	w0, _ := httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	require.Equal(t, http.StatusOK, w0.Code)
	cli.qpErr = errors.New("upstream 502")

	w, body := httpDo(t, r, http.MethodGet, "/api/v1/instances/main/quality-profiles")
	require.Equal(t, http.StatusOK, w.Code,
		"cached response MUST be returned when Sonarr fails on a hit-eligible key")
	assert.Equal(t, "hit", body["cache_status"])
	assert.Equal(t, int32(1), cli.qpCalls.Load(), "hit MUST NOT issue a Sonarr request")
}
