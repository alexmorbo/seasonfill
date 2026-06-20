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
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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
func (f *fakeSonarr) ListSeriesCache(_ context.Context, _ domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSonarr) GetSeries(_ context.Context, _ domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarr) ListEpisodes(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (f *fakeSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *fakeSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarr) GrabHistory(_ context.Context, _ domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (f *fakeSonarr) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
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

func newCheckerNoInstances(t *testing.T) *healthcheck.Checker {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	c := healthcheck.New(db, nil)
	c.Preflight(context.Background())
	return c
}

func setupRouter(t *testing.T, checker *healthcheck.Checker) *gin.Engine {
	t.Helper()
	r := gin.New()
	h := NewHealthHandler(checker)
	r.GET("/healthz", h.Live)
	r.GET("/readyz", h.Ready)
	return r
}

func TestHealthHandler_Live_AlwaysOK(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main"}, false))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, true, body["database"])
	// Body shape is deliberately narrow now — no instance state.
	_, hasSonarr := body["sonarr"]
	_, hasInstances := body["instances"]
	_, hasReasons := body["reasons"]
	assert.False(t, hasSonarr, "/readyz must not expose external-instance bool")
	assert.False(t, hasInstances, "/readyz must not expose instances array")
	assert.False(t, hasReasons, "/readyz must not expose reasons array")
}

func TestHealthHandler_Ready_SonarrDown_StillReady(t *testing.T) {
	t.Parallel()
	// Regression guard for the 2026-05-26 incident: an unauthorised
	// or unreachable Sonarr instance must NOT fail readiness — the pod
	// has to stay in the K8s Service so the operator can reach the UI
	// and fix the upstream config.
	r := setupRouter(t, newChecker(t, &fakeSonarr{name: "main", err: assertErr("boom")}, false))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "external Sonarr failure must not flip readiness")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, true, body["database"])
}

func TestHealthHandler_Ready_NoInstancesIsReady(t *testing.T) {
	t.Parallel()
	r := setupRouter(t, newCheckerNoInstances(t))
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "pristine deploy with zero instances must be ready")

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, true, body["database"])
}

func TestHealthHandler_Ready_DBDown(t *testing.T) {
	t.Parallel()
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
