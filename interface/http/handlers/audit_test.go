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
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
)

// --- harness --------------------------------------------------------------

type auditFixture struct {
	db     *gorm.DB
	scans  *repositories.ScanRepository
	decs   *repositories.DecisionRepository
	grabs  *repositories.GrabRepository
	router *gin.Engine
}

func newAuditFixture(t *testing.T, withAuth bool) *auditFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	scans := repositories.NewScanRepository(db)
	decs := repositories.NewDecisionRepository(db)
	grabs := repositories.NewGrabRepository(db)
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewAuditHandler(scans, decs, grabs, lg)

	r := gin.New()
	api := r.Group("/api/v1")
	if withAuth {
		sessionKey, skErr := crypto.DeriveSessionHMACKey("test-key")
		require.NoError(t, skErr)
		api.Use(middleware.RequireAuth("test-key", sessionKey))
	}
	api.GET("/scans", h.ListScans)
	api.GET("/scans/:id", h.GetScan)
	api.GET("/decisions", h.ListDecisions)
	api.GET("/grabs", h.ListGrabs)

	return &auditFixture{db: db, scans: scans, decs: decs, grabs: grabs, router: r}
}

func (f *auditFixture) seedScan(t *testing.T, instance, status string, createdAt time.Time) ports.ScanRecord {
	t.Helper()
	rec := ports.ScanRecord{
		ID:           uuid.New(),
		InstanceName: instance,
		Trigger:      "manual",
		StartedAt:    createdAt,
		Status:       status,
		DryRun:       true,
	}
	require.NoError(t, f.scans.Create(context.Background(), rec))
	require.NoError(t, f.db.Table("scan_runs").Where("id = ?", rec.ID.String()).Update("created_at", createdAt).Error)
	return rec
}

func (f *auditFixture) seedDecision(t *testing.T, scanRunID uuid.UUID, instance string, seriesID, season int, outcome decision.Outcome, createdAt time.Time) decision.Decision {
	t.Helper()
	d := decision.New(scanRunID, instance, "Hijack", seriesID, season)
	d.Outcome = outcome
	d.Reason = decision.ReasonSkipNoMissing
	d.CreatedAt = createdAt
	require.NoError(t, f.decs.Save(context.Background(), d))
	return d
}

// seedDecisionWithError seeds an error-outcome decision. Distinct
// helper (vs new param on seedDecision) keeps existing call sites
// untouched.
func (f *auditFixture) seedDecisionWithError(t *testing.T, scanRunID uuid.UUID, instance string, seriesID, season int, errDetail string, createdAt time.Time) decision.Decision {
	t.Helper()
	d := decision.New(scanRunID, instance, "Hijack", seriesID, season)
	d.Outcome = decision.OutcomeError
	d.Reason = decision.ReasonErrorFetchReleases
	d.ErrorDetail = errDetail
	d.CreatedAt = createdAt
	require.NoError(t, f.decs.Save(context.Background(), d))
	return d
}

func (f *auditFixture) seedGrab(t *testing.T, instance string, seriesID, season int, status grab.Status, createdAt time.Time) grab.Record {
	t.Helper()
	rec := grab.Record{
		ID:           uuid.New(),
		InstanceName: instance,
		SeriesID:     seriesID,
		SeriesTitle:  "Hijack",
		SeasonNumber: season,
		ReleaseGUID:  uuid.NewString(),
		ReleaseTitle: "S02 Pack",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       status,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	require.NoError(t, f.grabs.Create(context.Background(), rec))
	return rec
}

func (f *auditFixture) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

type listBody struct {
	Items      []map[string]any `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

func decodeList(t *testing.T, w *httptest.ResponseRecorder) listBody {
	t.Helper()
	var body listBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// --- /scans ---------------------------------------------------------------

func TestAuditHandler_AllListEndpoints_Empty(t *testing.T) {
	// One fixture, one walk over all three list endpoints — each should
	// return {items: [], next_cursor: ""}.
	f := newAuditFixture(t, false)
	for _, path := range []string{"/api/v1/scans", "/api/v1/decisions", "/api/v1/grabs"} {
		w := f.do(t, http.MethodGet, path)
		require.Equal(t, http.StatusOK, w.Code, path)
		body := decodeList(t, w)
		assert.Empty(t, body.Items, path)
		assert.Empty(t, body.NextCursor, path)
	}
}

func TestAuditHandler_ListScans_CursorWalk(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.seedScan(t, "main", "completed", base.Add(time.Duration(i)*time.Second))
	}

	// First page: 3 rows + next_cursor.
	w := f.do(t, http.MethodGet, "/api/v1/scans?limit=3")
	require.Equal(t, http.StatusOK, w.Code)
	first := decodeList(t, w)
	require.Len(t, first.Items, 3)
	require.NotEmpty(t, first.NextCursor)
	// D-3.A.6 — created_at must be rendered with correct value.
	wantCreatedAt := base.Add(4 * time.Second).UTC().Format(time.RFC3339)
	assert.Equal(t, wantCreatedAt, first.Items[0]["created_at"], "created_at value mismatch")
	assert.Equal(t, "completed", first.Items[0]["status"])

	// Second page: 2 rows + omitted next_cursor.
	w = f.do(t, http.MethodGet, "/api/v1/scans?limit=3&cursor="+first.NextCursor)
	second := decodeList(t, w)
	require.Len(t, second.Items, 2)
	assert.Empty(t, second.NextCursor)

	seen := map[string]bool{}
	for _, it := range append(first.Items, second.Items...) {
		id := it["id"].(string)
		assert.False(t, seen[id], "dup %s", id)
		seen[id] = true
	}
	assert.Len(t, seen, 5)
}

func TestAuditHandler_ListScans_InstanceAndStatusFilters(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f.seedScan(t, "main", "completed", base)
	f.seedScan(t, "main", "failed", base.Add(time.Second))
	f.seedScan(t, "secondary", "completed", base.Add(2*time.Second))

	w := f.do(t, http.MethodGet, "/api/v1/scans?instance=main&status=failed&limit=10")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "main", body.Items[0]["instance"])
	assert.Equal(t, "failed", body.Items[0]["status"])
}

func TestAuditHandler_ListScans_BadQueryParams(t *testing.T) {
	f := newAuditFixture(t, false)
	cases := []struct {
		query   string
		wantMsg string
	}{
		{"limit=0", "invalid limit"},
		{"limit=-1", "invalid limit"},
		{"limit=201", "invalid limit"},
		{"limit=not-a-number", "invalid limit"},
		{"cursor=!!!not-base64!!!", "invalid cursor"},
		{"from=not-a-time", "invalid from"},
		{"to=2026-99-99", "invalid to"},
	}
	for _, tc := range cases {
		w := f.do(t, http.MethodGet, "/api/v1/scans?"+tc.query)
		require.Equal(t, http.StatusBadRequest, w.Code, tc.query)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), tc.query)
		assert.Equal(t, tc.wantMsg, body["error"], tc.query)
	}
}

// --- /scans/:id -----------------------------------------------------------

func TestAuditHandler_GetScan_Found(t *testing.T) {
	f := newAuditFixture(t, false)
	rec := f.seedScan(t, "main", "completed", time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))

	w := f.do(t, http.MethodGet, "/api/v1/scans/"+rec.ID.String())
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, rec.ID.String(), body["id"])
	assert.Equal(t, "main", body["instance"])
}

func TestAuditHandler_GetScan_NotFound(t *testing.T) {
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/scans/"+uuid.NewString())
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "scan not found", body["error"])
}

func TestAuditHandler_GetScan_BadUUID(t *testing.T) {
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/scans/not-a-uuid")
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid id", body["error"])
}

// --- /decisions -----------------------------------------------------------

func TestAuditHandler_ListDecisions_CursorWalk(t *testing.T) {
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.seedDecision(t, scanRun, "main", 100+i, 1, decision.OutcomeSkip, base.Add(time.Duration(i)*time.Second))
	}

	w := f.do(t, http.MethodGet, "/api/v1/decisions?limit=3")
	require.Equal(t, http.StatusOK, w.Code)
	first := decodeList(t, w)
	require.Len(t, first.Items, 3)
	require.NotEmpty(t, first.NextCursor)

	w = f.do(t, http.MethodGet, "/api/v1/decisions?limit=3&cursor="+first.NextCursor)
	second := decodeList(t, w)
	require.Len(t, second.Items, 2)
	assert.Empty(t, second.NextCursor)
}

func TestAuditHandler_ListDecisions_CombinedFilters(t *testing.T) {
	f := newAuditFixture(t, false)
	scanA := uuid.New()
	scanB := uuid.New()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f.seedDecision(t, scanA, "main", 100, 1, decision.OutcomeGrab, base)
	f.seedDecision(t, scanA, "main", 100, 2, decision.OutcomeSkip, base.Add(time.Second))
	f.seedDecision(t, scanA, "main", 200, 1, decision.OutcomeSkip, base.Add(2*time.Second))
	f.seedDecision(t, scanB, "main", 100, 1, decision.OutcomeSkip, base.Add(3*time.Second))

	q := "scan_run_id=" + scanA.String() + "&instance=main&series_id=100"
	w := f.do(t, http.MethodGet, "/api/v1/decisions?"+q)
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 2)
	for _, it := range body.Items {
		assert.Equal(t, "main", it["instance"])
		assert.Equal(t, float64(100), it["series_id"])
		assert.Equal(t, scanA.String(), it["scan_run_id"])
	}
}

func TestAuditHandler_ListDecisions_BadQueryParams(t *testing.T) {
	f := newAuditFixture(t, false)
	cases := []struct {
		query   string
		wantMsg string
	}{
		{"series_id=not-an-int", "invalid series_id"},
		{"season_number=abc", "invalid season_number"},
		{"scan_run_id=not-a-uuid", "invalid scan_run_id"},
	}
	for _, tc := range cases {
		w := f.do(t, http.MethodGet, "/api/v1/decisions?"+tc.query)
		require.Equal(t, http.StatusBadRequest, w.Code, tc.query)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), tc.query)
		assert.Equal(t, tc.wantMsg, body["error"], tc.query)
	}
}

func TestAuditHandler_ListDecisions_SurfacesErrorDetail(t *testing.T) {
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	f.seedDecisionWithError(t, scanRun, "main", 100, 1,
		"sonarr: 503 service unavailable", base)

	w := f.do(t, http.MethodGet, "/api/v1/decisions")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "error", body.Items[0]["decision"])
	assert.Equal(t, "error", body.Items[0]["category"])
	assert.Equal(t, "sonarr: 503 service unavailable", body.Items[0]["error_detail"])
}

func TestAuditHandler_ListDecisions_OmitsEmptyErrorDetail(t *testing.T) {
	// Non-error decisions must NOT emit "error_detail" in the JSON —
	// the DTO field is omitempty so the wire stays clean.
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	f.seedDecision(t, scanRun, "main", 100, 1, decision.OutcomeSkip, base)

	w := f.do(t, http.MethodGet, "/api/v1/decisions")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	_, present := body.Items[0]["error_detail"]
	assert.False(t, present, "error_detail must be omitted on non-error decisions")
}

// --- /grabs ---------------------------------------------------------------

func TestAuditHandler_ListGrabs_CursorWalk(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.seedGrab(t, "main", 100+i, 1, grab.StatusGrabbed, base.Add(time.Duration(i)*time.Second))
	}

	w := f.do(t, http.MethodGet, "/api/v1/grabs?limit=3")
	require.Equal(t, http.StatusOK, w.Code)
	first := decodeList(t, w)
	require.Len(t, first.Items, 3)
	require.NotEmpty(t, first.NextCursor)

	w = f.do(t, http.MethodGet, "/api/v1/grabs?limit=3&cursor="+first.NextCursor)
	second := decodeList(t, w)
	require.Len(t, second.Items, 2)
	assert.Empty(t, second.NextCursor)
}

func TestAuditHandler_ListGrabs_CombinedFilters(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	f.seedGrab(t, "main", 100, 1, grab.StatusGrabbed, base)
	f.seedGrab(t, "main", 100, 2, grab.StatusGrabFailed, base.Add(time.Second))
	f.seedGrab(t, "main", 200, 1, grab.StatusGrabbed, base.Add(2*time.Second))
	f.seedGrab(t, "secondary", 100, 1, grab.StatusGrabbed, base.Add(3*time.Second))

	w := f.do(t, http.MethodGet, "/api/v1/grabs?instance=main&series_id=100&status=grabbed")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "main", body.Items[0]["instance"])
	assert.Equal(t, float64(100), body.Items[0]["series_id"])
	assert.Equal(t, "grabbed", body.Items[0]["status"])
}

func TestAuditHandler_ListGrabs_BadSeriesID(t *testing.T) {
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/grabs?series_id=oops")
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid series_id", body["error"])
}

// --- Auth -----------------------------------------------------------------

func TestAuditHandler_Auth(t *testing.T) {
	f := newAuditFixture(t, true)
	rec := f.seedScan(t, "main", "completed", time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	paths := []string{
		"/api/v1/scans",
		"/api/v1/scans/" + rec.ID.String(),
		"/api/v1/decisions",
		"/api/v1/grabs",
	}
	for _, path := range paths {
		// Missing X-Api-Key → 401.
		w := f.do(t, http.MethodGet, path)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "no key: "+path)

		// Valid X-Api-Key → 200.
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		req.Header.Set("X-Api-Key", "test-key")
		w = httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "valid key: "+path)
	}
}
