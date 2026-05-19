package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
)

type fakeSonarr struct {
	name string
	err  error
}

func (f *fakeSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	if f.err != nil {
		return ports.SystemStatus{}, f.err
	}
	return ports.SystemStatus{Version: "test"}, nil
}
func (f *fakeSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) Name() string { return f.name }

func newChecker(t *testing.T, sonarr ports.SonarrClient, closeDB bool) *healthcheck.Checker {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	if closeDB {
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	}
	c := healthcheck.New(db, []ports.SonarrClient{sonarr})
	c.Preflight(context.Background())
	return c
}

func setupRouter(t *testing.T, checker *healthcheck.Checker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewHealthHandler(checker)
	r.GET("/healthz", h.Live)
	r.GET("/readyz", h.Ready)
	return r
}

func TestHealthHandler_Live_AlwaysOK(t *testing.T) {
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main"}, false))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestHealthHandler_Ready_OK(t *testing.T) {
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main"}, false))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, true, body["database"])
}

func TestHealthHandler_Ready_AllSonarrDown(t *testing.T) {
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main", err: assertErr("boom")}, false))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body["status"])
	assert.Equal(t, false, body["sonarr"])
}

func TestHealthHandler_Ready_DBDown(t *testing.T) {
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main"}, true))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "unavailable", body["status"])
	assert.Equal(t, false, body["database"])
}

type assertErr string

func (a assertErr) Error() string { return string(a) }
