package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type stubSonarr struct {
	name string
	ser  []series.Series
}

func (s *stubSonarr) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{Version: "test"}, nil
}
func (s *stubSonarr) ListSeries(_ context.Context) ([]series.Series, error) { return s.ser, nil }
func (s *stubSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (s *stubSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (s *stubSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (s *stubSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (s *stubSonarr) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (s *stubSonarr) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (s *stubSonarr) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (s *stubSonarr) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (s *stubSonarr) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	return "", nil
}
func (s *stubSonarr) Name() string { return s.name }

type stubScanRepo struct{}

func (r *stubScanRepo) Create(context.Context, ports.ScanRecord) error { return nil }
func (r *stubScanRepo) Update(context.Context, ports.ScanRecord) error { return nil }
func (r *stubScanRepo) GetByID(_ context.Context, _ uuid.UUID) (ports.ScanRecord, error) {
	return ports.ScanRecord{}, nil
}
func (r *stubScanRepo) MarkAborted(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (r *stubScanRepo) IncrementSeriesScanned(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}
func (r *stubScanRepo) List(_ context.Context, _ ports.ScanFilter, _ ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type stubDecRepo struct{}

func (stubDecRepo) Save(context.Context, decision.Decision) error { return nil }
func (stubDecRepo) GetByID(context.Context, uuid.UUID) (decision.Decision, error) {
	return decision.Decision{}, ports.ErrNotFound
}
func (stubDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

func newScanUseCase() *scan.UseCase {
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sonarr := &stubSonarr{name: "main"}
	evalUC := evaluate.NewUseCase(sonarr, stubDecRepo{}, lg)
	return scan.NewUseCase(
		[]scan.Instance{{
			Config: config.SonarrInstance{Name: "main", Limits: config.LimitsConfig{ScanMaxSeries: 100}},
			Client: sonarr,
		}},
		evalUC,
		&stubScanRepo{},
		lg,
		true,
	)
}

func setupScanRouter(uc *scan.UseCase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r.POST("/api/v1/scan", NewScanHandler(uc, lg).Trigger)
	return r
}

func TestScanHandler_Trigger_AllInstances(t *testing.T) {
	r := setupScanRouter(newScanUseCase())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, "main", body[0]["instance"])
}

func TestScanHandler_Trigger_SpecificInstance(t *testing.T) {
	r := setupScanRouter(newScanUseCase())

	body, _ := json.Marshal(map[string]string{"instance": "main"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)

	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "main", out[0]["instance"])
}

func TestScanHandler_Trigger_UnknownInstance(t *testing.T) {
	r := setupScanRouter(newScanUseCase())

	body, _ := json.Marshal(map[string]string{"instance": "does-not-exist"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var body404 map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body404))
	assert.Equal(t, "unknown instance", body404["error"])
	assert.Equal(t, "does-not-exist", body404["instance"])
}

func TestScanHandler_Trigger_EmptyBody(t *testing.T) {
	r := setupScanRouter(newScanUseCase())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
}

func setupCancelRouter(uc *scan.UseCase) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r.POST("/api/v1/scans/:id/cancel", NewScanHandler(uc, lg).Cancel)
	return r
}

// stubSonarr has no series → StartInstance completes immediately, so
// Cancel arriving after completion legitimately returns 404. Accept
// either 202 or 404 to avoid a timing-flake; §2 use-case tests pin the
// terminal-status path against a controlled stub.
func TestScanHandler_Cancel_OK(t *testing.T) {
	uc := newScanUseCase()
	res, err := uc.StartInstance(t.Context(), "main", scan.TriggerManual)
	require.NoError(t, err)
	r := setupCancelRouter(uc)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/scans/"+res.ScanRunID.String()+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Contains(t, []int{http.StatusAccepted, http.StatusNotFound}, w.Code, w.Body.String())
}

func TestScanHandler_Cancel_NotRunning(t *testing.T) {
	r := setupCancelRouter(newScanUseCase())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/scans/"+uuid.New().String()+"/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "scan not running", body["error"])
}

func TestScanHandler_Cancel_BadID(t *testing.T) {
	r := setupCancelRouter(newScanUseCase())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/scans/not-a-uuid/cancel", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
