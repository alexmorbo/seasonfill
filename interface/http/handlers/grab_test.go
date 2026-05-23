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
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
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

type fakeGrabRepo struct {
	mu     sync.Mutex
	stored []grab.Record
}

// Create enforces the 4-tuple unique constraint in-memory to match the DB
// unique index — exercises the race-recovery path in TestGrabHandler_ByDecision_RaceIdempotent.
func (f *fakeGrabRepo) Create(_ context.Context, r grab.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.stored {
		if e.InstanceName == r.InstanceName && e.SeriesID == r.SeriesID &&
			e.SeasonNumber == r.SeasonNumber && e.ReleaseGUID == r.ReleaseGUID {
			return repositories.ErrGrabDuplicate
		}
	}
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
func (f *fakeGrabRepo) FindExisting4Tuple(_ context.Context, inst string, sid, season int, guid string) (grab.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.stored {
		if r.InstanceName == inst && r.SeriesID == sid && r.SeasonNumber == season && r.ReleaseGUID == guid {
			return r, nil
		}
	}
	return grab.Record{}, ports.ErrNotFound
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
	gin.SetMode(gin.TestMode)
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
	h := NewGrabHandler(dec, gr, cd, grabUC, map[string]scan.Instance{"alpha": inst}, lg)
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

func TestGrabHandler_ByDecision_AlreadyGrabbed(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	require.NoError(t, f.grabRepo.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 122, SeasonNumber: 2,
		ReleaseGUID: "g1", Status: grab.StatusGrabbed,
		ScanRunID: d.ScanRunID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	assertErrBody(t, f.do(t, d.ID.String()), http.StatusConflict, "already grabbed")
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

// Two concurrent POSTs: exactly one grab_records row; responses 200/200 or 200/409.
func TestGrabHandler_ByDecision_RaceIdempotent(t *testing.T) {
	f := newGrabFixture(t, nil)
	d := f.seedEligible(t)
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = f.do(t, d.ID.String()) }()
	}
	wg.Wait()
	f.grabRepo.mu.Lock()
	rowCount := len(f.grabRepo.stored)
	f.grabRepo.mu.Unlock()
	assert.Equal(t, 1, rowCount, "exactly one grab_records row")
	ok, conflict := 0, 0
	for _, w := range results {
		switch w.Code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("unexpected status %d: %s", w.Code, w.Body.String())
		}
	}
	assert.GreaterOrEqual(t, ok, 1, "at least one 200")
	assert.Equal(t, 2, ok+conflict, "all 200 or 409")
}
