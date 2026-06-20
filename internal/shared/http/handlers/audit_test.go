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
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	media "github.com/alexmorbo/seasonfill/internal/mediaproxy/domain"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// --- harness --------------------------------------------------------------

type auditFixture struct {
	db          *gorm.DB
	scans       *catalogpersistence.ScanRepository
	decs        *grabpersistence.DecisionRepository
	grabs       *grabpersistence.GrabRepository
	seriesCache *catalogpersistence.SeriesCacheRepository
	router      *gin.Engine
}

func newAuditFixture(t *testing.T, withAuth bool) *auditFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	// Story 352: the EnsurePending kick runs in a background
	// goroutine which acquires a fresh sqlite connection — :memory:
	// connections get isolated databases, so without single-conn
	// pinning the goroutine's writes land on an unmigrated DB.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	scans := catalogpersistence.NewScanRepository(db)
	decs := grabpersistence.NewDecisionRepository(db)
	grabs := grabpersistence.NewGrabRepository(db)
	seriesCache := catalogpersistence.NewSeriesCacheRepository(db, enrichpersistence.NewSeriesRepository(db))
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	// Note: WithSeriesCache is wired here so the per-test slug
	// fixture works. Tests that don't seed series_cache still see
	// title_slug omitted from the wire (omitempty + empty slug
	// from the lookup map miss).
	h := NewAuditHandler(scans, decs, grabs, lg).WithSeriesCache(seriesCache)

	r := gin.New()
	// F-2c-1: typed-error middleware so handler c.Error(err) reaches
	// the JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(lg))
	api := r.Group("/api/v1")
	if withAuth {
		sessionKey, skErr := crypto.DeriveSessionHMACKey("test-key")
		require.NoError(t, skErr)
		api.Use(middleware.RequireAuth("test-key", sessionKey))
	}
	api.GET("/scans", h.ListScans)
	api.GET("/scans/:id", h.GetScan)
	api.GET("/decisions", h.ListDecisions)
	api.GET("/decisions/:id", h.GetDecision)
	api.GET("/grabs", h.ListGrabs)

	return &auditFixture{db: db, scans: scans, decs: decs, grabs: grabs, seriesCache: seriesCache, router: r}
}

// withMediaPending swaps the AuditHandler in the fixture's router
// for one that also wires a MediaAssetsRepository as the
// CatalogMediaPendingWriter. Story 352 — verifies /grabs enqueues
// EnsurePending after building the wire DTOs.
//
// Returns the underlying *MediaAssetsRepository so tests can poll
// media_assets for the row.
func (f *auditFixture) withMediaPending(t *testing.T) *enrichpersistence.MediaAssetsRepository {
	t.Helper()
	mediaRepo := enrichpersistence.NewMediaAssetsRepository(f.db)
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := NewAuditHandler(f.scans, f.decs, f.grabs, lg).
		WithSeriesCache(f.seriesCache).
		WithMediaPending(mediaRepo)
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(lg))
	api := r.Group("/api/v1")
	api.GET("/scans", h.ListScans)
	api.GET("/scans/:id", h.GetScan)
	api.GET("/decisions", h.ListDecisions)
	api.GET("/decisions/:id", h.GetDecision)
	api.GET("/grabs", h.ListGrabs)
	f.router = r
	return mediaRepo
}

func (f *auditFixture) seedScan(t *testing.T, instance domain.InstanceName, status string, createdAt time.Time) ports.ScanRecord {
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

func (f *auditFixture) seedDecision(t *testing.T, scanRunID uuid.UUID, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, outcome decision.Outcome, createdAt time.Time) decision.Decision {
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
func (f *auditFixture) seedDecisionWithError(t *testing.T, scanRunID uuid.UUID, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, errDetail string, createdAt time.Time) decision.Decision {
	t.Helper()
	d := decision.New(scanRunID, instance, "Hijack", seriesID, season)
	d.Outcome = decision.OutcomeError
	d.Reason = decision.ReasonErrorFetchReleases
	d.ErrorDetail = errDetail
	d.CreatedAt = createdAt
	require.NoError(t, f.decs.Save(context.Background(), d))
	return d
}

func (f *auditFixture) seedGrab(t *testing.T, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, status grab.Status, createdAt time.Time) grab.Record {
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
	for i := range 5 {
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
	// F-2c-1: typed-error middleware emits the slug on `error` and the
	// human-readable text on `message`.
	assert.Equal(t, "scan_run_not_found", body["error"])
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

// --- GET /decisions/:id (N-4) --------------------------------------------

func TestAuditHandler_GetDecision_Found(t *testing.T) {
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	d := f.seedDecision(t, scanRun, "main", 100, 3, decision.OutcomeSkip, base)

	w := f.do(t, http.MethodGet, "/api/v1/decisions/"+d.ID.String())
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, d.ID.String(), body["id"])
	assert.Equal(t, scanRun.String(), body["scan_run_id"])
	assert.Equal(t, "main", body["instance"])
	assert.Equal(t, float64(100), body["series_id"])
	assert.Equal(t, float64(3), body["season_number"])
	assert.Equal(t, "skip", body["decision"])
	// 091a / F-P2-2: skip-path decisions carry no intent. The DTO
	// emits null (omitempty + nil pointer drops the key entirely).
	_, hasIntent := body["intent"]
	assert.False(t, hasIntent, "skip decisions must omit intent from wire")
}

// TestAuditHandler_GetDecision_ReturnsIntent — 091a / F-P2-2: a
// decision row carrying Intent surfaces it on GET /decisions/:id with
// the right fields and chosen_because string.
func TestAuditHandler_GetDecision_ReturnsIntent(t *testing.T) {
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	d := decision.New(scanRun, "main", "Hijack", 100, 3)
	d.Outcome = decision.OutcomeGrab
	d.Reason = decision.ReasonGrabSelectedDryRun
	d.DryRunWouldGrab = true
	intent := decision.NewIntent(
		[]int{10, 11}, []int{1, 2, 3, 4, 5, 6, 7, 8, 9},
		decision.ChosenBecauseHighestScore,
		"score 88 vs alternates 64",
	)
	d.Intent = &intent
	d.CreatedAt = base
	require.NoError(t, f.decs.Save(context.Background(), d))

	w := f.do(t, http.MethodGet, "/api/v1/decisions/"+d.ID.String())
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	raw, ok := body["intent"].(map[string]any)
	require.True(t, ok, "intent must be a JSON object")
	assert.Equal(t, "highest_score", raw["chosen_because"])
	assert.Equal(t, "score 88 vs alternates 64", raw["chosen_reason_detail"])

	targets, ok := raw["target_episodes"].([]any)
	require.True(t, ok)
	require.Len(t, targets, 2)
	assert.Equal(t, float64(10), targets[0])
	assert.Equal(t, float64(11), targets[1])
	had, ok := raw["had_episodes"].([]any)
	require.True(t, ok)
	assert.Len(t, had, 9)
}

func TestAuditHandler_GetDecision_NotFound(t *testing.T) {
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/decisions/"+uuid.NewString())
	require.Equal(t, http.StatusNotFound, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	// F-2c-1: typed-error middleware emits the slug on `error`.
	assert.Equal(t, "decision_not_found", body["error"])
}

func TestAuditHandler_GetDecision_BadUUID(t *testing.T) {
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/decisions/not-a-uuid")
	require.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "invalid id", body["error"])
}

// `dto.Decision` is the DTO type for `toDecisionDTO`; no need to
// import — `assert` against the json shape is sufficient (and matches
// the existing GetScan_Found pattern).
var _ = dto.Decision{}

func TestAuditHandler_ListDecisions_CursorWalk(t *testing.T) {
	f := newAuditFixture(t, false)
	scanRun := uuid.New()
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		f.seedDecision(t, scanRun, "main", domain.SonarrSeriesID(100+i), 1, decision.OutcomeSkip, base.Add(time.Duration(i)*time.Second))
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
	for i := range 5 {
		f.seedGrab(t, "main", domain.SonarrSeriesID(100+i), 1, grab.StatusGrabbed, base.Add(time.Duration(i)*time.Second))
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

// --- 043a: Grab DTO extensions -----------------------------------------------

func TestAuditHandler_ListGrabs_ExposesTorrentHashAndChainPointers(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	parent := grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Severance",
		SeasonNumber: 2,
		ReleaseGUID:  "g_parent",
		ReleaseTitle: "Severance.S02.PACK",
		IndexerID:    1,
		IndexerName:  "indexer",
		Status:       grab.StatusImported,
		ScanRunID:    uuid.New(),
		CreatedAt:    base,
		UpdatedAt:    base,
	}
	hash := domain.QbitHash("0123456789abcdef0123456789abcdef01234567")
	parent.TorrentHash = &hash
	require.NoError(t, f.grabs.Create(context.Background(), parent))

	child := grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Severance",
		SeasonNumber: 2,
		ReleaseGUID:  "g_child",
		ReleaseTitle: "Severance.S02.PACK.v2",
		IndexerID:    1,
		IndexerName:  "indexer",
		Status:       grab.StatusImported,
		ScanRunID:    uuid.New(),
		ReplayOfID:   &parent.ID,
		CreatedAt:    base.Add(time.Hour),
		UpdatedAt:    base.Add(time.Hour),
	}
	require.NoError(t, f.grabs.Create(context.Background(), child))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []struct {
			ID          string   `json:"id"`
			TorrentHash *string  `json:"torrent_hash"`
			ReplayOfID  *string  `json:"replay_of_id"`
			ReplayedBy  []string `json:"replayed_by"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 2)

	gotChild, gotParent := resp.Items[0], resp.Items[1] // created_at DESC
	require.NotNil(t, gotParent.TorrentHash)
	assert.Equal(t, string(hash), *gotParent.TorrentHash)
	require.Len(t, gotParent.ReplayedBy, 1)
	assert.Equal(t, child.ID.String(), gotParent.ReplayedBy[0])
	assert.Nil(t, gotParent.ReplayOfID)
	require.NotNil(t, gotChild.ReplayOfID)
	assert.Equal(t, parent.ID.String(), *gotChild.ReplayOfID)
	assert.Empty(t, gotChild.ReplayedBy)
}

// --- F-P2-3: replay_kind derivation -----------------------------------------

func TestAuditHandler_ListGrabs_DerivesReplayKind(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	mk := func(title string, parsed *grab.Parsed, parentID *uuid.UUID, offset time.Duration) grab.Record {
		return grab.Record{
			ID:           uuid.New(),
			InstanceName: "main",
			SeriesID:     500,
			SeriesTitle:  "X",
			SeasonNumber: 1,
			ReleaseGUID:  uuid.NewString(),
			ReleaseTitle: title,
			IndexerID:    1,
			IndexerName:  "rt",
			Status:       grab.StatusImported,
			ScanRunID:    uuid.New(),
			ReplayOfID:   parentID,
			Parsed:       parsed,
			CreatedAt:    base.Add(offset),
			UpdatedAt:    base.Add(offset),
		}
	}

	// Pair 1: quality bump 1080p -> 2160p.
	p1 := mk("p1", &grab.Parsed{Resolution: 1080}, nil, 0)
	c1 := mk("c1", &grab.Parsed{Resolution: 2160}, &p1.ID, time.Minute)
	// Pair 2: dub gained, same resolution.
	p2 := mk("p2", &grab.Parsed{Resolution: 1080}, nil, 2*time.Minute)
	c2 := mk("c2", &grab.Parsed{Resolution: 1080, Dub: "MVO"}, &p2.ID, 3*time.Minute)
	// Pair 3: replay but nothing changed.
	p3 := mk("p3", &grab.Parsed{Resolution: 1080}, nil, 4*time.Minute)
	c3 := mk("c3", &grab.Parsed{Resolution: 1080}, &p3.ID, 5*time.Minute)
	// Pair 4: parent has no Parsed at all — dub axis disqualified.
	p4 := mk("p4", nil, nil, 6*time.Minute)
	c4 := mk("c4", &grab.Parsed{Resolution: 1080}, &p4.ID, 7*time.Minute)
	// Pair 5: HDR-only gain at the same resolution counts as quality.
	p5 := mk("p5", &grab.Parsed{Resolution: 2160}, nil, 8*time.Minute)
	c5 := mk("c5", &grab.Parsed{Resolution: 2160, HDRFlags: []string{"HDR10"}}, &p5.ID, 9*time.Minute)

	for _, r := range []grab.Record{p1, c1, p2, c2, p3, c3, p4, c4, p5, c5} {
		require.NoError(t, f.grabs.Create(ctx, r))
	}

	w := f.do(t, http.MethodGet, "/api/v1/grabs?limit=50")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []struct {
			ID           string `json:"id"`
			ReleaseTitle string `json:"release_title"`
			ReplayKind   string `json:"replay_kind"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	byTitle := map[string]string{}
	for _, it := range resp.Items {
		byTitle[it.ReleaseTitle] = it.ReplayKind
	}
	assert.Equal(t, "replay_quality", byTitle["c1"], "1080p -> 2160p must be quality")
	assert.Equal(t, "replay_dub", byTitle["c2"], "dub gained must be dub")
	assert.Equal(t, "replay_other", byTitle["c3"], "no axis differs must be other")
	assert.Equal(t, "replay_other", byTitle["c4"], "parent unparsed must be other")
	assert.Equal(t, "replay_quality", byTitle["c5"], "HDR gain must be quality")
	assert.Equal(t, "", byTitle["p1"], "root grab must omit replay_kind")
	assert.Equal(t, "", byTitle["p3"], "root grab must omit replay_kind")
}

func TestAuditHandler_ListGrabs_ReplayKindOmittedWhenPrimary(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	ctx := context.Background()
	rec := makeGrabRecord(t)
	require.NoError(t, f.grabs.Create(ctx, rec))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), `"replay_kind"`,
		"primary rows must omit replay_kind from the wire")
}

// makeGrabRecord is a helper to construct a test grab.Record with all
// required fields populated.
func makeGrabRecord(t *testing.T) grab.Record {
	t.Helper()
	return grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Severance",
		SeasonNumber: 2,
		ReleaseGUID:  "test-guid",
		ReleaseTitle: "Severance.S02.WEBDL-1080p",
		IndexerID:    1,
		IndexerName:  "tracker.x",
		Quality:      "WEBDL-1080p",
		Status:       grab.StatusGrabbed,
		ScanRunID:    uuid.New(),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func TestAuditHandler_ListGrabs_EmptyDB_OK(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Items []any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Items)
}

func TestAuditHandler_ListGrabs_ExposesSizeBytes(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	ctx := context.Background()
	rec := makeGrabRecord(t)
	sz := int64(13_325_829_734)
	rec.SizeBytes = &sz
	require.NoError(t, f.grabs.Create(ctx, rec))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	var resp dto.GrabList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	require.NotNil(t, resp.Items[0].SizeBytes)
	assert.Equal(t, sz, *resp.Items[0].SizeBytes)
}

func TestAuditHandler_ListGrabs_SizeBytesOmittedWhenNil(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	ctx := context.Background()
	rec := makeGrabRecord(t) // SizeBytes nil
	require.NoError(t, f.grabs.Create(ctx, rec))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	// Raw JSON inspection — size_bytes must be absent from the wire.
	assert.NotContains(t, w.Body.String(), `"size_bytes"`,
		"nil SizeBytes must omit the field from the wire")
}

// TestAuditHandler_ListGrabs_IncludesTitleSlugFromCache covers 116.
// When a series_cache row exists for the (instance, series_id) tuple
// of a grab, the wire response carries title_slug from that row —
// the authoritative Sonarr slug (correctly expands `&` to `-and-`,
// etc.) rather than the FE's lossy client-side slugifier.
func TestAuditHandler_ListGrabs_IncludesTitleSlugFromCache(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	// Seed: one grab pointing at series 122 + a series_cache row
	// with the authoritative slug Sonarr would have stamped.
	rec := f.seedGrab(t, "main", 122, 1, grab.StatusImported, base)
	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 122,
		Title:          "Your Friends & Neighbors",
		TitleSlug:      "your-friends-and-neighbors",
		Monitored:      true,
	}))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, rec.ID.String(), body.Items[0]["id"])
	assert.Equal(t, "your-friends-and-neighbors", body.Items[0]["title_slug"])
}

// TestAuditHandler_ListGrabs_OmitsTitleSlugWhenCacheMisses covers
// the degradation path — no series_cache row for the (instance,
// series_id) tuple means the wire omits the key entirely (omitempty
// kicks in). The SPA then falls back to its client-side slugifier.
func TestAuditHandler_ListGrabs_OmitsTitleSlugWhenCacheMisses(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	// Seed a grab but NO series_cache row for that (instance,
	// series_id) tuple. The series_cache row below is for a
	// DIFFERENT series — proves the lookup is keyed correctly,
	// not just "any row in the table".
	f.seedGrab(t, "main", 122, 1, grab.StatusImported, base)
	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 999, // unrelated series
		Title:          "Other",
		TitleSlug:      "other",
		Monitored:      true,
	}))

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	_, present := body.Items[0]["title_slug"]
	assert.False(t, present, "title_slug should be omitted when no series_cache row matches")
}

// seedCanonPosterAsset stamps poster_asset on the canon row resolved
// for (instance, sonarrID). The grabs handler derives the wire
// poster_hash deterministically from this raw path — independent of
// any media_assets row state.
func seedCanonPosterAsset(t *testing.T, f *auditFixture, instance string, sonarrID int, path string) {
	t.Helper()
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", instance, sonarrID,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID, "series_cache row must resolve a canon series_id")
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", path).Error)
}

// When a series_cache row exists and the canon poster_asset is set,
// the grabs wire response carries poster_hash derived from the
// synthetic w342 CDN URL — independent of any media_assets row state.
// One ListActiveByInstance call covers both title_slug and the
// derived poster_hash (no fanout).
func TestAuditHandler_ListGrabs_IncludesPosterHashDerivedFromCanonPath(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	path := "/poster.jpg"
	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, path),
	)

	rec := f.seedGrab(t, "main", 122, 1, grab.StatusImported, base)
	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 122,
		Title:          "Your Friends & Neighbors",
		TitleSlug:      "your-friends-and-neighbors",
		Monitored:      true,
	}))
	seedCanonPosterAsset(t, f, "main", 122, path)

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, rec.ID.String(), body.Items[0]["id"])
	assert.Equal(t, expectedHash, body.Items[0]["poster_hash"],
		"canon path on series → deterministic hash projected to wire (no media_assets dependency)")
	// Slug still ships alongside — the single repo call covers both.
	assert.Equal(t, "your-friends-and-neighbors", body.Items[0]["title_slug"])
}

// Pending media_assets rows must NOT suppress the wire poster_hash —
// the derivation works off the canon path alone, so the FE can request
// /media/<hash> immediately and the media handler's on-demand fetch
// fills the bytes. This is the central fix for the "monogram until
// Series Detail" bug.
func TestAuditHandler_ListGrabs_PosterHashUnaffectedByPendingMediaRow(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	path := "/pending.jpg"
	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, path),
	)

	f.seedGrab(t, "main", 222, 1, grab.StatusImported, base.Add(time.Minute))

	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 222,
		Title:          "Pending Poster",
		TitleSlug:      "pending-poster",
		Monitored:      true,
	}))
	seedCanonPosterAsset(t, f, "main", 222, path)
	// media_assets row exists but is still in 'pending'. Previously
	// this suppressed the wire poster_hash; the fix is to ignore the
	// media row state and derive from the canon path.
	require.NoError(t, f.db.Create(&database.MediaAssetModel{
		Hash:      "feedface11feedface11feedface11feedface11feedface11feedface11feed",
		SourceURL: "https://image.tmdb.org/t/p/w342/pending.jpg",
		Kind:      "poster_w342",
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
	}).Error)

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 1)
	assert.Equal(t, expectedHash, body.Items[0]["poster_hash"],
		"pending media_assets row must NOT suppress the canon-derived poster_hash")
}

// poster_hash MUST be omitted from the wire when either the cache row
// is missing OR the canon poster_asset is NULL — those are the cases
// where the FE legitimately falls back to a monogram placeholder.
func TestAuditHandler_ListGrabs_OmitsPosterHashWhenNoCanonPath(t *testing.T) {
	f := newAuditFixture(t, false)
	base := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	f.seedGrab(t, "main", 222, 1, grab.StatusImported, base.Add(time.Minute))
	f.seedGrab(t, "main", 333, 1, grab.StatusImported, base)

	// series 222: cache row exists, but poster_asset stays NULL on the
	// canon row — handler derivation returns nil → field omitted.
	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "main",
		SonarrSeriesID: 222,
		Title:          "No Canon Path",
		TitleSlug:      "no-canon-path",
		Monitored:      true,
	}))
	// series 333: no series_cache row at all — collectGrabCacheFields
	// map misses → hashes[key] is nil pointer.

	w := f.do(t, http.MethodGet, "/api/v1/grabs")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeList(t, w)
	require.Len(t, body.Items, 2)
	for _, it := range body.Items {
		_, present := it["poster_hash"]
		assert.False(t, present,
			"poster_hash must be omitted from wire when canon path is NULL or cache row missing (series %v)", it["series_id"])
	}
	// Sanity: omitempty drops the field entirely, never emits null.
	assert.NotContains(t, w.Body.String(), `"poster_hash":null`)
}

// Story 352: /grabs projects an eager poster_hash via
// collectGrabCacheFields and must land a pending media_assets row
// for every projected hash. The kick runs in a background goroutine
// after the response commits; the test polls media_assets until the
// row appears (2-second deadline).
func TestAudit_ListGrabs_EnsuresPendingMediaAssets(t *testing.T) {
	t.Parallel()
	f := newAuditFixture(t, false)
	mediaRepo := f.withMediaPending(t)

	now := time.Now().UTC()
	scan := f.seedScan(t, "homelab", "completed", now)
	_ = f.seedDecision(t, scan.ID, "homelab", 42, 1, decision.OutcomeGrab, now)
	g := f.seedGrab(t, "homelab", 42, 1, grab.StatusGrabbed, now)
	_ = g

	// Seed a series_cache row with a canon poster_asset for the
	// (instance, series_id) pair the grab references. The audit
	// handler reads series_cache to derive title_slug + poster_hash.
	// The PosterAsset is stored on the canon `series` row, not the
	// series_cache row — Upsert does not persist e.PosterAsset
	// (see resolveOrCreateCanon docs), so we stamp the canon row
	// directly after upsert.
	year := 2024
	posterPath := "/audit-grab.jpg"
	require.NoError(t, f.seriesCache.Upsert(context.Background(), series.CacheEntry{
		InstanceName:   "homelab",
		SonarrSeriesID: 42,
		Title:          "Hijack",
		TitleSlug:      "hijack",
		Year:           &year,
		Monitored:      true,
	}))
	var sc database.SeriesCacheModel
	require.NoError(t, f.db.Where(
		"instance_name = ? AND sonarr_series_id = ?", "homelab", 42,
	).First(&sc).Error)
	require.NotNil(t, sc.SeriesID)
	require.NoError(t, f.db.Model(&database.SeriesModel{}).
		Where("id = ?", *sc.SeriesID).
		Update("poster_asset", posterPath).Error)

	expectedHash := appmedia.HashFromURL(
		appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, posterPath),
	)

	w := f.do(t, http.MethodGet, "/api/v1/grabs?instance=homelab")
	require.Equal(t, http.StatusOK, w.Code)

	deadline := time.Now().Add(2 * time.Second)
	var asset media.Asset
	for time.Now().Before(deadline) {
		a, err := mediaRepo.Get(context.Background(), expectedHash)
		if err == nil {
			asset = a
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, expectedHash, asset.Hash, "media_assets row must exist for the eager hash from the grab series_cache lookup")
	assert.Equal(t, "poster_w342", asset.Kind)
	assert.Equal(t, media.StatusPending, asset.Status)
}
