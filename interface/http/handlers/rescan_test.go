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

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
)

type rescanFakeDec struct {
	mu    sync.Mutex
	store map[uuid.UUID]decision.Decision
}

func (f *rescanFakeDec) Save(_ context.Context, d decision.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.store == nil {
		f.store = map[uuid.UUID]decision.Decision{}
	}
	f.store[d.ID] = d
	return nil
}
func (f *rescanFakeDec) GetByID(_ context.Context, id uuid.UUID) (decision.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.store[id]; ok {
		return d, nil
	}
	return decision.Decision{}, ports.ErrNotFound
}
func (f *rescanFakeDec) UpdateSupersededBy(_ context.Context, id, newID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.store[id]
	if !ok {
		return ports.ErrNotFound
	}
	d.SupersededByID = &newID
	f.store[id] = d
	return nil
}
func (f *rescanFakeDec) List(context.Context, ports.DecisionFilter, ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	return nil, nil, nil
}

type rescanFakeGrab struct{ stored []grab.Record }

func (f *rescanFakeGrab) Create(_ context.Context, r grab.Record) error {
	f.stored = append(f.stored, r)
	return nil
}
func (f *rescanFakeGrab) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	out := make([]grab.Record, len(f.stored))
	copy(out, f.stored)
	return out, nil, nil
}
func (f *rescanFakeGrab) MatchLatest(context.Context, ports.MatchKey) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}
func (f *rescanFakeGrab) UpdateStatus(context.Context, uuid.UUID, grab.Status, string) error {
	return nil
}
func (f *rescanFakeGrab) FindExisting4Tuple(context.Context, string, int, int, string) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}

type rescanFakeSonarr struct{ releases []release.Release }

func (f *rescanFakeSonarr) GetSeries(_ context.Context, id int) (series.Series, error) {
	return series.Series{ID: id, Title: "Severance", Monitored: true, QualityProfile: 7}, nil
}
func (f *rescanFakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return []series.Episode{
		{Number: 1, Monitored: true, HasFile: true, QualityID: 19},
		{Number: 2, Monitored: true, HasFile: false},
	}, nil
}
func (f *rescanFakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (f *rescanFakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return f.releases, nil
}
func (f *rescanFakeSonarr) GetQualityProfile(_ context.Context, id int) (ports.QualityProfile, error) {
	return ports.QualityProfile{ID: id, Name: "Any",
		Items: []ports.QualityItem{{ID: 19, Name: "WEBDL-1080p", Order: 1, Weight: 1}}}, nil
}

// Shim methods — required by ports.SonarrClient, unused by rescan.
func (f *rescanFakeSonarr) SystemStatus(context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (f *rescanFakeSonarr) ListSeries(context.Context) ([]series.Series, error)   { return nil, nil }
func (f *rescanFakeSonarr) ListIndexers(context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *rescanFakeSonarr) ListTags(context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *rescanFakeSonarr) GrabHistory(context.Context, int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ForceGrab(context.Context, string, int) (string, error) { return "DL", nil }
func (f *rescanFakeSonarr) Name() string                                           { return "alpha" }

type rescanFixture struct {
	dec    *rescanFakeDec
	gr     *rescanFakeGrab
	router *gin.Engine
}

func newRescanFixture(t *testing.T, releases []release.Release) *rescanFixture {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dec, gr := &rescanFakeDec{}, &rescanFakeGrab{}
	sn := &rescanFakeSonarr{releases: releases}
	ev := evaluate.NewUseCase(sn, dec, lg)
	inst := scan.Instance{Config: config.SonarrInstance{Name: "alpha"}, Client: sn}
	m := map[string]scan.Instance{"alpha": inst}
	uc := rescan.NewUseCase(dec, gr, ev, func() map[string]scan.Instance { return m }, lg)
	h := NewRescanHandler(uc, lg)
	r := gin.New()
	r.POST("/api/v1/decisions/:id/rescan", h.ByDecision)
	return &rescanFixture{dec: dec, gr: gr, router: r}
}

func (f *rescanFixture) seed(t *testing.T, withGUID bool) decision.Decision {
	t.Helper()
	d := decision.New(uuid.New(), "alpha", "Severance", 1, 1)
	d.Outcome, d.Reason = decision.OutcomeSkip, decision.ReasonSkipNoReleases
	if withGUID {
		d.Outcome, d.Reason = decision.OutcomeGrab, decision.ReasonGrabSelectedDryRun
		d.DryRunWouldGrab = true
		d.Selected = &release.Scored{Release: release.Release{GUID: "g-orig", Title: "orig"}}
	}
	require.NoError(t, f.dec.Save(context.Background(), d))
	return d
}

func (f *rescanFixture) do(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/decisions/"+id+"/rescan", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func TestRescan_OK_ReturnsNewDecision(t *testing.T) {
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEqual(t, d.ID.String(), body["id"])
	assert.Equal(t, d.ScanRunID.String(), body["scan_run_id"],
		"017 §3.4: new decision shares scan_run_id")
}

func TestRescan_OriginalMarkedSuperseded(t *testing.T) {
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	require.Equal(t, http.StatusOK, f.do(t, d.ID.String()).Code)
	loaded, _ := f.dec.GetByID(context.Background(), d.ID)
	require.NotNil(t, loaded.SupersededByID)
}

func TestRescan_BadID(t *testing.T) {
	require.Equal(t, http.StatusBadRequest,
		newRescanFixture(t, nil).do(t, "not-a-uuid").Code)
}

func TestRescan_NotFound(t *testing.T) {
	require.Equal(t, http.StatusNotFound,
		newRescanFixture(t, nil).do(t, uuid.New().String()).Code)
}

func TestRescan_AlreadySuperseded_409(t *testing.T) {
	f := newRescanFixture(t, nil)
	d := f.seed(t, false)
	require.NoError(t, f.dec.UpdateSupersededBy(context.Background(), d.ID, uuid.New()))
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "already superseded")
}

func TestRescan_AlreadyExecuted_409(t *testing.T) {
	f := newRescanFixture(t, nil)
	d := f.seed(t, true)
	require.NoError(t, f.gr.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 1, SeasonNumber: 1,
		ReleaseGUID: "g-orig", Status: grab.StatusGrabbed,
		ScanRunID: d.ScanRunID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body["error"], "already executed")
}

func TestRescan_UnknownInstance_404(t *testing.T) {
	f := newRescanFixture(t, nil)
	d := f.seed(t, false)
	d.InstanceName = "ghost"
	require.NoError(t, f.dec.Save(context.Background(), d))
	require.Equal(t, http.StatusNotFound, f.do(t, d.ID.String()).Code)
}

// 017 §3.8 — last-writer-wins: no crash, ≥1 OK, original superseded.
func TestRescan_ConcurrentRequests_NoCrash(t *testing.T) {
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 4)
	for i := 0; i < 4; i++ {
		i := i
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = f.do(t, d.ID.String()) }()
	}
	wg.Wait()
	ok := 0
	for _, w := range results {
		if w.Code == http.StatusOK {
			ok++
		}
	}
	assert.GreaterOrEqual(t, ok, 1)
	loaded, _ := f.dec.GetByID(context.Background(), d.ID)
	require.NotNil(t, loaded.SupersededByID)
}
