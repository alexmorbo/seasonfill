package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/rescan"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/release"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/grab/app/evaluate"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	"github.com/alexmorbo/seasonfill/internal/grab/domain/decision"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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
func (f *rescanFakeDec) ClearSupersededBy(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.store[id]
	if !ok {
		return ports.ErrNotFound
	}
	d.SupersededByID = nil
	f.store[id] = d
	return nil
}
func (f *rescanFakeDec) List(context.Context, ports.DecisionFilter, ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	return nil, nil, nil
}
func (f *rescanFakeDec) UpdateIntent(context.Context, uuid.UUID, *decision.Intent) error {
	return nil
}

type rescanFakeGrab struct {
	mu     sync.Mutex
	stored []grab.Record
}

func (f *rescanFakeGrab) Create(_ context.Context, r grab.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, r)
	return nil
}
func (f *rescanFakeGrab) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *rescanFakeGrab) UpdateTorrentHash(context.Context, uuid.UUID, string) error {
	return nil
}

func (f *rescanFakeGrab) FindLatestSuccessByHash(_ context.Context, _ string) (grab.Record, error) {
	panic("fake FindLatestSuccessByHash unexpectedly called - this stub is not configured")
}

func (f *rescanFakeGrab) CreateReplay(_ context.Context, rec grab.Record, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored = append(f.stored, rec)
	return nil
}

func (f *rescanFakeGrab) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}

func (f *rescanFakeGrab) ListReplaysOf(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}

func (f *rescanFakeGrab) UpdateSizeBytes(_ context.Context, _ uuid.UUID, _ int64) error {
	return nil
}

func (f *rescanFakeGrab) GetByID(_ context.Context, _ uuid.UUID) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}

func (f *rescanFakeGrab) CountReplaysSince(_ context.Context, _ domain.InstanceName, _ time.Time) (int, error) {
	return 0, nil
}

func (f *rescanFakeGrab) CountReplaysAll(_ context.Context, _ domain.InstanceName) (int, error) {
	return 0, nil
}

func (f *rescanFakeGrab) CountImportedEpisodes(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (int, error) {
	return 0, nil
}
func (f *rescanFakeGrab) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]grab.Record, error) {
	return nil, nil
}
func (f *rescanFakeGrab) UpdateParsed(_ context.Context, _ uuid.UUID, _ *grab.Parsed, _ time.Time) error {
	return nil
}

type rescanFakeScans struct {
	mu      sync.Mutex
	created map[uuid.UUID]ports.ScanRecord
	updated map[uuid.UUID]ports.ScanRecord
}

func (f *rescanFakeScans) Create(_ context.Context, rec ports.ScanRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created == nil {
		f.created = map[uuid.UUID]ports.ScanRecord{}
	}
	f.created[rec.ID] = rec
	return nil
}
func (f *rescanFakeScans) Update(_ context.Context, rec ports.ScanRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updated == nil {
		f.updated = map[uuid.UUID]ports.ScanRecord{}
	}
	f.updated[rec.ID] = rec
	return nil
}
func (f *rescanFakeScans) GetByID(_ context.Context, id uuid.UUID) (ports.ScanRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.updated[id]; ok {
		return r, nil
	}
	if r, ok := f.created[id]; ok {
		return r, nil
	}
	return ports.ScanRecord{}, ports.ErrNotFound
}
func (f *rescanFakeScans) MarkAborted(context.Context, uuid.UUID, string) error { return nil }
func (f *rescanFakeScans) IncrementSeriesScanned(context.Context, uuid.UUID, int) error {
	return nil
}
func (f *rescanFakeScans) List(context.Context, ports.ScanFilter, ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	return nil, nil, nil
}

type rescanFakeSonarr struct{ releases []release.Release }

func (f *rescanFakeSonarr) GetSeries(_ context.Context, id domain.SonarrSeriesID) (series.Series, error) {
	return series.Series{ID: id, Title: "Severance", Monitored: true, QualityProfile: 7}, nil
}
func (f *rescanFakeSonarr) ListEpisodes(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]series.Episode, error) {
	return []series.Episode{
		{Number: 1, Monitored: true, HasFile: true, QualityID: 19},
		{Number: 2, Monitored: true, HasFile: false},
	}, nil
}

func (f *rescanFakeSonarr) ListEpisodesBySeries(_ context.Context, _ domain.SonarrSeriesID) ([]series.Episode, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ListEpisodeFiles(_ context.Context, _ domain.SonarrSeriesID) (map[int]int, error) {
	return map[int]int{}, nil
}
func (f *rescanFakeSonarr) ListEpisodeFilesBySeason(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) SearchReleases(_ context.Context, _ domain.SonarrSeriesID, _ int) ([]release.Release, error) {
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
func (f *rescanFakeSonarr) ListSeries(context.Context) ([]series.Series, error) { return nil, nil }
func (f *rescanFakeSonarr) ListSeriesCache(context.Context, domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ListIndexers(context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *rescanFakeSonarr) ListTags(context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *rescanFakeSonarr) GrabHistory(context.Context, domain.SonarrSeriesID) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ForceGrab(context.Context, string, int) (string, error) { return "DL", nil }
func (f *rescanFakeSonarr) ParseRelease(context.Context, string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (f *rescanFakeSonarr) Name() string { return "alpha" }

// fakeInflight is a minimal scan.InflightController for handler tests.
// It uses a sync.Map-equivalent to track per-instance ownership and
// exposes a WaitGroup so test cases can drain in-flight goroutines.
type fakeInflight struct {
	mu       sync.Mutex
	busy     map[domain.InstanceName]uuid.UUID
	cancels  map[domain.InstanceName]context.CancelFunc
	wg       sync.WaitGroup
	preFail  atomic.Bool // when true, AcquireInstance returns ErrScanAlreadyRunning
	acquired atomic.Int32
}

func (f *fakeInflight) AcquireInstance(name domain.InstanceName, scanID uuid.UUID) error {
	if f.preFail.Load() {
		return scan.ErrScanAlreadyRunning
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy == nil {
		f.busy = map[domain.InstanceName]uuid.UUID{}
	}
	if _, ok := f.busy[name]; ok {
		return scan.ErrScanAlreadyRunning
	}
	f.busy[name] = scanID
	f.acquired.Add(1)
	return nil
}
func (f *fakeInflight) ReleaseInstance(name domain.InstanceName) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, name)
	delete(f.cancels, name)
}
func (f *fakeInflight) SetInflightCancel(name domain.InstanceName, cancel context.CancelFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancels == nil {
		f.cancels = map[domain.InstanceName]context.CancelFunc{}
	}
	f.cancels[name] = cancel
}
func (f *fakeInflight) BackgroundWG() *sync.WaitGroup { return &f.wg }

type rescanFixture struct {
	dec      *rescanFakeDec
	gr       *rescanFakeGrab
	scans    *rescanFakeScans
	inflight *fakeInflight
	router   *gin.Engine
}

func newRescanFixture(t *testing.T, releases []release.Release) *rescanFixture {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dec, gr := &rescanFakeDec{}, &rescanFakeGrab{}
	scans := &rescanFakeScans{}
	inflight := &fakeInflight{}
	sn := &rescanFakeSonarr{releases: releases}
	ev := evaluate.NewUseCase(sn, dec, lg)
	inst := scan.Instance{Config: config.SonarrInstance{Name: "alpha"}, Client: sn}
	m := map[string]scan.Instance{"alpha": inst}
	uc := rescan.NewUseCase(dec, gr, scans, inflight, ev, func() map[string]scan.Instance { return m }, lg)
	h := NewRescanHandler(uc, lg)
	r := gin.New()
	// F-2c-1: middleware so c.Error → JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(lg))
	r.POST("/api/v1/decisions/:id/rescan", h.ByDecision)
	return &rescanFixture{dec: dec, gr: gr, scans: scans, inflight: inflight, router: r}
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

// drain blocks until every goroutine the inflight WG knows about exits.
// Handler tests use this so they can assert on terminal scan-row state
// without sleep-polling.
func (f *rescanFixture) drain(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { f.inflight.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rescan goroutine did not drain within 5s")
	}
}

func TestRescan_OK_Returns202WithScanTriggerItem(t *testing.T) {
	t.Parallel()
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	var body []dto.ScanTriggerItem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.NotEmpty(t, body[0].ScanRunID)
	assert.Equal(t, domain.InstanceName("alpha"), body[0].InstanceName)
	assert.Equal(t, "running", body[0].Status)
	// scan_run_id is fresh (not the original's)
	assert.NotEqual(t, d.ScanRunID.String(), body[0].ScanRunID,
		"async rescan creates a NEW scan_run_id")
	f.drain(t)
}

func TestRescan_OriginalMarkedSuperseded(t *testing.T) {
	t.Parallel()
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	require.Equal(t, http.StatusAccepted, f.do(t, d.ID.String()).Code)
	f.drain(t)
	loaded, _ := f.dec.GetByID(context.Background(), d.ID)
	require.NotNil(t, loaded.SupersededByID,
		"supersede pointer must be set before the goroutine starts")
}

func TestRescan_BadID(t *testing.T) {
	t.Parallel()
	require.Equal(t, http.StatusBadRequest,
		newRescanFixture(t, nil).do(t, "not-a-uuid").Code)
}

func TestRescan_NotFound(t *testing.T) {
	t.Parallel()
	require.Equal(t, http.StatusNotFound,
		newRescanFixture(t, nil).do(t, uuid.New().String()).Code)
}

func TestRescan_AlreadySuperseded_409(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	f := newRescanFixture(t, nil)
	d := f.seed(t, false)
	d.InstanceName = "ghost"
	require.NoError(t, f.dec.Save(context.Background(), d))
	require.Equal(t, http.StatusNotFound, f.do(t, d.ID.String()).Code)
}

func TestRescan_ScanAlreadyRunning_409(t *testing.T) {
	t.Parallel()
	f := newRescanFixture(t, nil)
	d := f.seed(t, false)
	f.inflight.preFail.Store(true)
	w := f.do(t, d.ID.String())
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var body dto.ScanConflictResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "SCAN_IN_PROGRESS", body.Code)
	assert.Equal(t, "scan already running", body.Error)
	assert.Equal(t, domain.InstanceName("alpha"), body.Instance)
	// no scan row was created, supersede pointer untouched
	loaded, _ := f.dec.GetByID(context.Background(), d.ID)
	assert.Nil(t, loaded.SupersededByID)
}

// 017 §3.8 — last-writer-wins: concurrent requests on the same decision
// must not crash. With per-instance single-flight one wins with 202, the
// rest 409 (SCAN_IN_PROGRESS) until the goroutine releases. We assert ≥1
// 202 and ≥1 409 (the runner-up); supersede pointer set after drain.
func TestRescan_ConcurrentRequests_NoCrash(t *testing.T) {
	t.Parallel()
	f := newRescanFixture(t, []release.Release{
		{GUID: "g-new", Title: "fresh", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	})
	d := f.seed(t, false)
	var wg sync.WaitGroup
	results := make([]*httptest.ResponseRecorder, 4)
	for i := range 4 {
		wg.Go(func() {
			results[i] = f.do(t, d.ID.String())
		})
	}
	wg.Wait()
	accepted := 0
	for _, w := range results {
		if w.Code == http.StatusAccepted {
			accepted++
		}
	}
	assert.GreaterOrEqual(t, accepted, 1)
	f.drain(t)
	loaded, _ := f.dec.GetByID(context.Background(), d.ID)
	require.NotNil(t, loaded.SupersededByID)
}
