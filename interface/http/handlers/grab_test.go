package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appgrab "github.com/alexmorbo/seasonfill/application/grab"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// --- fakes ----------------------------------------------------------------

type fakeDecRepo struct {
	mu    sync.Mutex
	store map[uuid.UUID]decision.Decision
}

func (f *fakeDecRepo) Save(_ context.Context, d decision.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.store == nil {
		f.store = map[uuid.UUID]decision.Decision{}
	}
	f.store[d.ID] = d
	return nil
}
func (f *fakeDecRepo) GetByID(_ context.Context, id uuid.UUID) (decision.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.store[id]; ok {
		return d, nil
	}
	return decision.Decision{}, ports.ErrNotFound
}
func (f *fakeDecRepo) List(_ context.Context, _ ports.DecisionFilter, _ ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	return nil, nil, nil
}

func (f *fakeDecRepo) UpdateSupersededBy(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (f *fakeDecRepo) ClearSupersededBy(context.Context, uuid.UUID) error { return nil }

type fakeGrabRepo struct {
	mu     sync.Mutex
	stored []grab.Record
}

func (f *fakeGrabRepo) Create(_ context.Context, r grab.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, r)
	return nil
}
func (f *fakeGrabRepo) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]grab.Record, len(f.stored))
	copy(out, f.stored)
	return out, nil, nil
}
func (f *fakeGrabRepo) MatchLatest(_ context.Context, _ ports.MatchKey) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}
func (f *fakeGrabRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ grab.Status, _ string) error {
	return nil
}

func (f *fakeGrabRepo) UpdateTorrentHash(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (f *fakeGrabRepo) FindLatestSuccessByHash(_ context.Context, _ string) (grab.Record, error) {
	panic("fake FindLatestSuccessByHash unexpectedly called - this stub is not configured")
}

func (f *fakeGrabRepo) CreateReplay(_ context.Context, rec grab.Record, _ uuid.UUID) error {
	panic("fake CreateReplay unexpectedly called - this stub is not configured")
}

func (f *fakeGrabRepo) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	panic("fake SetReplayOfID unexpectedly called - this stub is not configured")
}

type fakeCooldowns struct {
	active map[cooldown.Scope]map[string]bool
}

func (f *fakeCooldowns) Set(_ context.Context, _ cooldown.Cooldown) error { return nil }
func (f *fakeCooldowns) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (f *fakeCooldowns) FilterActive(_ context.Context, scope cooldown.Scope, keys []string, _ time.Time) ([]cooldown.Cooldown, error) {
	out := []cooldown.Cooldown{}
	for _, k := range keys {
		if f.active[scope][k] {
			out = append(out, cooldown.Cooldown{Scope: scope, Key: k})
		}
	}
	return out, nil
}
func (f *fakeCooldowns) Sweep(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

type fakeOrigins struct{}

func (fakeOrigins) Get(_ context.Context, _ string, _, _ int) (ports.OriginRelease, bool, error) {
	return ports.OriginRelease{}, false, nil
}
func (fakeOrigins) Upsert(_ context.Context, _ ports.OriginRelease) error { return nil }

type stubClassifier struct{}

func (stubClassifier) IsTransient(_ error) bool { return false }
func (stubClassifier) Is4xx(_ error) bool       { return true }

type stubSonarrGrab struct {
	*fakeSonarr
	forceErr error
}

func (s *stubSonarrGrab) ForceGrab(_ context.Context, _ string, _ int) (string, error) {
	if s.forceErr != nil {
		return "", s.forceErr
	}
	return "DL-123", nil
}

// --- harness --------------------------------------------------------------

type grabFixture struct {
	dec       *fakeDecRepo
	grabRepo  *fakeGrabRepo
	cooldowns *fakeCooldowns
	router    *gin.Engine
}

func newGrabFixture(t *testing.T, forceErr error) *grabFixture {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dec, gr, cd := &fakeDecRepo{}, &fakeGrabRepo{}, &fakeCooldowns{}
	sn := &stubSonarrGrab{fakeSonarr: &fakeSonarr{name: "alpha"}, forceErr: forceErr}
	grabUC := appgrab.NewUseCase(gr, cd, fakeOrigins{}, stubClassifier{}, lg)
	inst := scan.Instance{
		Config: config.SonarrInstance{Name: "alpha",
			Retry:    config.RetryConfig{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
			Cooldown: config.CooldownConfig{SeriesAfterGrab: time.Hour, GUIDAfterFailedGrab: time.Hour},
		},
		Client: sn,
	}
	reg := InstanceRegistry{Load: func() map[string]scan.Instance {
		return map[string]scan.Instance{"alpha": inst}
	}}
	h := NewGrabHandler(dec, gr, cd, grabUC, reg, lg)
	r := gin.New()
	r.POST("/api/v1/decisions/:id/grab", h.ByDecision)
	return &grabFixture{dec: dec, grabRepo: gr, cooldowns: cd, router: r}
}

func (f *grabFixture) seedEligible(t *testing.T) decision.Decision {
	t.Helper()
	d := decision.New(uuid.New(), "alpha", "Severance", 122, 2)
	d.Outcome = decision.OutcomeGrab
	d.Reason = decision.ReasonGrabSelectedDryRun
	d.DryRunWouldGrab = true
	d.Selected = &release.Scored{
		Release: release.Release{GUID: "g1", Title: "S02 Pack",
			IndexerID: 3, IndexerName: "RT", QualityName: "WEBDL-1080p"},
		Coverage: 9,
	}
	require.NoError(t, f.dec.Save(context.Background(), d))
	return d
}

func (f *grabFixture) do(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/decisions/"+id+"/grab", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func assertErrBody(t *testing.T, w *httptest.ResponseRecorder, code int, contains string) {
	t.Helper()
	require.Equal(t, code, w.Code, w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], contains)
}

// --- tests ----------------------------------------------------------------

func TestGrabHandler_ByDecision_OK(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "alpha", body["instance"])
	assert.Equal(t, "g1", body["release_guid"])
	assert.Equal(t, "grabbed", body["status"])
}

func TestGrabHandler_ByDecision_BadID(t *testing.T) {
	f := newGrabFixture(t, nil)
	w := f.do(t, "not-a-uuid")
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGrabHandler_ByDecision_NotFound(t *testing.T) {
	f := newGrabFixture(t, nil)
	assertErrBody(t, f.do(t, uuid.New().String()), http.StatusNotFound, "decision not found")
}

func TestGrabHandler_ByDecision_Ineligible(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*decision.Decision)
		code   int
		body   string
	}{
		{"not-grab", func(d *decision.Decision) { d.Outcome = decision.OutcomeSkip }, http.StatusBadRequest, "did not select"},
		{"no-guid", func(d *decision.Decision) { d.Selected = nil }, http.StatusBadRequest, "did not select"},
		{"already-executed", func(d *decision.Decision) { d.DryRunWouldGrab = false }, http.StatusConflict, "already executed"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := newGrabFixture(t, nil)
			d := f.seedEligible(t)
			tc.mutate(&d)
			require.NoError(t, f.dec.Save(context.Background(), d))
			assertErrBody(t, f.do(t, d.ID.String()), tc.code, tc.body)
		})
	}
}

// TestGrabHandler_ByDecision_AlreadyInFlight: a non-terminal grab on
// the same release still returns 409 (the fast-path is kept). Story
// 038 narrowed the rule — only non-terminal rows block.
func TestGrabHandler_ByDecision_AlreadyInFlight(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	require.NoError(t, f.grabRepo.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 122, SeasonNumber: 2,
		ReleaseGUID: "g1", Status: grab.StatusGrabbed,
		ScanRunID: d.ScanRunID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	assertErrBody(t, f.do(t, d.ID.String()), http.StatusConflict, "already grabbed")
}

// TestGrabHandler_ByDecision_PriorTerminalAllowsRegrab: when a previous
// attempt on the same release reached a terminal status
// (grab_failed / import_failed / imported), the user can press the
// button again and get a fresh row. No 409, two rows in store.
func TestGrabHandler_ByDecision_PriorTerminalAllowsRegrab(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	require.NoError(t, f.grabRepo.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 122, SeasonNumber: 2,
		ReleaseGUID: "g1", Status: grab.StatusGrabFailed,
		ScanRunID: d.ScanRunID, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}))

	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	f.grabRepo.mu.Lock()
	defer f.grabRepo.mu.Unlock()
	assert.Len(t, f.grabRepo.stored, 2, "fresh row must be appended; prior terminal row is preserved")
}

func TestGrabHandler_ByDecision_CooldownBlocked(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	f.cooldowns.active = map[cooldown.Scope]map[string]bool{
		cooldown.ScopeSeries: {cooldown.SeriesKey("alpha", 122, 2): true},
	}
	assertErrBody(t, f.do(t, d.ID.String()), http.StatusConflict, "blocked by cooldown")
}

func TestGrabHandler_ByDecision_SonarrUnauthorized(t *testing.T) {
	f := newGrabFixture(t, domain.ErrInstanceUnauthorized)
	d := f.seedEligible(t)
	assertErrBody(t, f.do(t, d.ID.String()), http.StatusBadGateway, "sonarr unauthorized")
}

func TestGrabHandler_ByDecision_DoublePost_TwoRows(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)

	w1 := f.do(t, d.ID.String())
	require.Equal(t, http.StatusOK, w1.Code, w1.Body.String())

	// Flip the first row to a terminal status so the in-flight
	// fast-path does not return 409 on the second POST.
	f.grabRepo.mu.Lock()
	require.Len(t, f.grabRepo.stored, 1)
	f.grabRepo.stored[0].Status = grab.StatusImportFailed
	f.grabRepo.mu.Unlock()

	w2 := f.do(t, d.ID.String())
	require.Equal(t, http.StatusOK, w2.Code, w2.Body.String())

	f.grabRepo.mu.Lock()
	defer f.grabRepo.mu.Unlock()
	require.Len(t, f.grabRepo.stored, 2, "two POSTs must produce two rows")
	assert.NotEqual(t, f.grabRepo.stored[0].ID, f.grabRepo.stored[1].ID)
}
