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
func (s *stubSonarr) ListSeriesCache(_ context.Context, _ string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (s *stubSonarr) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (s *stubSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (s *stubSonarr) ListEpisodesBySeries(_ context.Context, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (s *stubSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (s *stubSonarr) ListEpisodeFilesBySeason(_ context.Context, _, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
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
func (s *stubSonarr) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
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

func (stubDecRepo) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (stubDecRepo) ClearSupersededBy(context.Context, uuid.UUID) error { return nil }

func (stubDecRepo) UpdateIntent(context.Context, uuid.UUID, *decision.Intent) error {
	return nil
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

// TestScanHandler_Trigger_EmptyBody_StillScans — Story 121b §E:
// empty body MUST keep the "scan all instances" contract. The
// io.EOF branch is the documented shape used by `curl -X POST`
// without a body and the cron-trigger scheduler.
func TestScanHandler_Trigger_EmptyBody_StillScans(t *testing.T) {
	r := setupScanRouter(newScanUseCase())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code,
		"empty body must trigger scan-all, not 400")
}

// TestScanHandler_Trigger_MalformedJSON_Returns400 — Story 121b §E:
// malformed JSON body must 400, not silently degrade to scan-all.
func TestScanHandler_Trigger_MalformedJSON_Returns400(t *testing.T) {
	r := setupScanRouter(newScanUseCase())
	body := bytes.NewReader([]byte(`{"instance": "homelab"`)) // missing }
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid JSON body")
}

// TestScanHandler_Trigger_TypeMismatchOnOptionalField_Returns400 —
// Story 121b §E: type-mismatch (e.g. instance=42 instead of string)
// MUST 400 not silently degrade.
func TestScanHandler_Trigger_TypeMismatchOnOptionalField_Returns400(t *testing.T) {
	r := setupScanRouter(newScanUseCase())
	body := bytes.NewReader([]byte(`{"instance": 42}`))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestScanHandler_Trigger_DryRunOverride_NilOmitted — when the JSON
// body omits dry_run, the use case receives nil and the persisted
// ScanRecord follows the instance default. This pins the
// backward-compatibility contract: an old client that never knew about
// dry_run sees identical behaviour.
func TestScanHandler_Trigger_DryRunOverride_NilOmitted(t *testing.T) {
	uc := newScanUseCase()
	r := setupScanRouter(uc)

	body, _ := json.Marshal(map[string]any{"instance": "main"})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "main", out[0]["instance"])
	// newScanUseCase wires global dry-run=true with no instance override,
	// so the effective scan must be dry. We cannot inspect ScanRecord
	// from the stub (it discards Create), so behavioural assertion lives
	// in the use-case tests above; here we only assert the wire shape.
}

// TestScanHandler_Trigger_DryRunOverride_ForceTrue — request body sets
// dry_run=true. The handler must accept the field without 400-ing and
// return 202.
func TestScanHandler_Trigger_DryRunOverride_ForceTrue(t *testing.T) {
	uc := newScanUseCase()
	r := setupScanRouter(uc)

	body, _ := json.Marshal(map[string]any{"instance": "main", "dry_run": true})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "main", out[0]["instance"])
}

// TestScanHandler_Trigger_DryRunOverride_ForceFalse — request body sets
// dry_run=false. The handler must accept this even when the instance
// is configured dry (the "Force real grab" path); the use-case test
// `TestStartInstanceWithDryRun_ForceFalse` covers the behavioural
// assertion that ScanRecord.DryRun = false.
func TestScanHandler_Trigger_DryRunOverride_ForceFalse(t *testing.T) {
	uc := newScanUseCase()
	r := setupScanRouter(uc)

	body, _ := json.Marshal(map[string]any{"instance": "main", "dry_run": false})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	require.Len(t, out, 1)
	assert.Equal(t, "main", out[0]["instance"])
}

func setupCancelRouter(uc *scan.UseCase) *gin.Engine {
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
