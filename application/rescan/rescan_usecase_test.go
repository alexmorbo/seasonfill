package rescan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/evaluate"
	"github.com/alexmorbo/seasonfill/application/ports"
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

func (f *rescanFakeGrab) FindLatestSuccessByHash(context.Context, string) (grab.Record, error) {
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

func (f *rescanFakeGrab) CountReplaysSince(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

func (f *rescanFakeGrab) CountReplaysAll(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (f *rescanFakeGrab) CountImportedEpisodes(_ context.Context, _ string, _, _ int) (int, error) {
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

type rescanFakeSonarr struct {
	releases []release.Release
	failOn   string // "search" -> SearchReleases returns errSonarrSearch
	// block, when non-nil, causes GetSeries to block until the channel is
	// closed. This lets tests gate the goroutine before making pre-drain
	// assertions, keeping those assertions race-free on any scheduler.
	block chan struct{}
}

var errSonarrSearch = errors.New("sonarr search exploded")

func (f *rescanFakeSonarr) GetSeries(_ context.Context, id int) (series.Series, error) {
	if f.block != nil {
		<-f.block
	}
	return series.Series{ID: id, Title: "Severance", Monitored: true, QualityProfile: 7}, nil
}
func (f *rescanFakeSonarr) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return []series.Episode{
		{Number: 1, Monitored: true, HasFile: true, QualityID: 19},
		{Number: 2, Monitored: true, HasFile: false},
	}, nil
}

func (f *rescanFakeSonarr) ListEpisodesBySeries(_ context.Context, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return map[int]int{}, nil
}
func (f *rescanFakeSonarr) ListEpisodeFilesBySeason(_ context.Context, _, _ int) ([]ports.EpisodeFileDetail, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	if f.failOn == "search" {
		return nil, errSonarrSearch
	}
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
func (f *rescanFakeSonarr) ListSeriesCache(context.Context, string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ListIndexers(context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *rescanFakeSonarr) ListTags(context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *rescanFakeSonarr) GrabHistory(context.Context, int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *rescanFakeSonarr) ParseRelease(context.Context, string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (f *rescanFakeSonarr) ForceGrab(context.Context, string, int) (string, error) { return "DL", nil }
func (f *rescanFakeSonarr) Name() string                                           { return "alpha" }

// fakeInflight: minimal scan.InflightController + drain hook.
type fakeInflight struct {
	mu      sync.Mutex
	busy    map[string]uuid.UUID
	cancels map[string]context.CancelFunc
	wg      sync.WaitGroup
	preFail atomic.Bool
}

func (f *fakeInflight) AcquireInstance(name string, scanID uuid.UUID) error {
	if f.preFail.Load() {
		return scan.ErrScanAlreadyRunning
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy == nil {
		f.busy = map[string]uuid.UUID{}
	}
	if _, ok := f.busy[name]; ok {
		return scan.ErrScanAlreadyRunning
	}
	f.busy[name] = scanID
	return nil
}
func (f *fakeInflight) ReleaseInstance(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.busy, name)
	delete(f.cancels, name)
}
func (f *fakeInflight) SetInflightCancel(name string, cancel context.CancelFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancels == nil {
		f.cancels = map[string]context.CancelFunc{}
	}
	f.cancels[name] = cancel
}
func (f *fakeInflight) BackgroundWG() *sync.WaitGroup { return &f.wg }

func newUC(t *testing.T, sn *rescanFakeSonarr) (*UseCase, *rescanFakeDec, *rescanFakeGrab, *rescanFakeScans, *fakeInflight) {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dec, gr := &rescanFakeDec{}, &rescanFakeGrab{}
	scans := &rescanFakeScans{}
	inflight := &fakeInflight{}
	ev := evaluate.NewUseCase(sn, dec, lg)
	inst := scan.Instance{Config: config.SonarrInstance{Name: "alpha"}, Client: sn}
	m := map[string]scan.Instance{"alpha": inst}
	return NewUseCase(dec, gr, scans, inflight, ev,
		func() map[string]scan.Instance { return m }, lg), dec, gr, scans, inflight
}

func seedOriginal(t *testing.T, dec *rescanFakeDec, withGUID bool) decision.Decision {
	t.Helper()
	d := decision.New(uuid.New(), "alpha", "Severance", 1, 1)
	d.Outcome, d.Reason = decision.OutcomeSkip, decision.ReasonSkipNoReleases
	if withGUID {
		d.Outcome, d.Reason = decision.OutcomeGrab, decision.ReasonGrabSelectedDryRun
		d.DryRunWouldGrab = true
		d.Selected = &release.Scored{Release: release.Release{GUID: "g-orig", Title: "orig"}}
	}
	require.NoError(t, dec.Save(context.Background(), d))
	return d
}

func drain(t *testing.T, inflight *fakeInflight) {
	t.Helper()
	done := make(chan struct{})
	go func() { inflight.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rescan goroutine did not drain within 5s")
	}
}

func TestStart_HappyPath_CreatesScanAndSupersedes(t *testing.T) {
	t.Parallel()
	// gate blocks the goroutine inside GetSeries until we release it,
	// making the pre-drain assertions race-free regardless of scheduler.
	gate := make(chan struct{})
	sn := &rescanFakeSonarr{
		releases: []release.Release{
			{GUID: "g-new", Title: "rescan-pick", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
		},
		block: gate,
	}
	uc, dec, _, scans, inflight := newUC(t, sn)
	original := seedOriginal(t, dec, false)

	res, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, res.ScanRunID)
	assert.NotEqual(t, original.ScanRunID, res.ScanRunID,
		"async rescan creates a NEW scan_run_id (breaks 017 §3.4)")
	assert.Equal(t, "alpha", res.Instance)
	assert.Equal(t, "running", res.Status)

	// Scan row created with trigger=rescan, status=running BEFORE the
	// goroutine drains. The gate guarantees the goroutine hasn't called
	// finalizeAsCompleted yet.
	created, err := scans.GetByID(context.Background(), res.ScanRunID)
	require.NoError(t, err)
	assert.Equal(t, "running", created.Status)
	assert.Equal(t, string(scan.TriggerRescan), created.Trigger)
	assert.Equal(t, "alpha", created.InstanceName)
	assert.False(t, created.DryRun, "rescan is never dry")

	// Supersede pointer is set before the goroutine runs (rescan pre-applies).
	loaded, _ := dec.GetByID(context.Background(), original.ID)
	require.NotNil(t, loaded.SupersededByID)
	preAllocatedID := *loaded.SupersededByID

	// Release the gate so the goroutine can proceed, then drain.
	close(gate)
	drain(t, inflight)

	// Goroutine persisted the new decision under the pre-allocated id.
	persisted, err := dec.GetByID(context.Background(), preAllocatedID)
	require.NoError(t, err)
	assert.Equal(t, res.ScanRunID, persisted.ScanRunID,
		"new decision points at the NEW scan_run_id")

	// Scan row finalised as completed.
	finalRec, err := scans.GetByID(context.Background(), res.ScanRunID)
	require.NoError(t, err)
	assert.Equal(t, "completed", finalRec.Status)
	require.NotNil(t, finalRec.FinishedAt)
}

func TestStart_RollsBackSupersedeOnEvaluatorError(t *testing.T) {
	t.Parallel()
	sn := &rescanFakeSonarr{failOn: "search"}
	uc, dec, _, scans, inflight := newUC(t, sn)
	original := seedOriginal(t, dec, false)

	res, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.NoError(t, err, "prelude must succeed; failure is async")
	drain(t, inflight)

	finalRec, err := scans.GetByID(context.Background(), res.ScanRunID)
	require.NoError(t, err)
	assert.Equal(t, "failed", finalRec.Status)
	assert.Contains(t, finalRec.ErrorMessage, "sonarr search exploded")

	loaded, _ := dec.GetByID(context.Background(), original.ID)
	assert.Nil(t, loaded.SupersededByID,
		"supersede pointer must be rolled back when the goroutine fails")

	// Inflight slot released.
	assert.Empty(t, inflight.busy)
}

func TestStart_ReturnsConflictWhenAcquireFails(t *testing.T) {
	t.Parallel()
	sn := &rescanFakeSonarr{releases: []release.Release{
		{GUID: "g-new", Title: "rescan-pick", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	}}
	uc, dec, _, scans, inflight := newUC(t, sn)
	original := seedOriginal(t, dec, false)
	inflight.preFail.Store(true)

	_, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, scan.ErrScanAlreadyRunning))

	// No scan row, no supersede.
	assert.Empty(t, scans.created)
	loaded, _ := dec.GetByID(context.Background(), original.ID)
	assert.Nil(t, loaded.SupersededByID)
}

func TestStart_AlreadySuperseded(t *testing.T) {
	t.Parallel()
	uc, dec, _, scans, _ := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, false)
	require.NoError(t, dec.UpdateSupersededBy(context.Background(), original.ID, uuid.New()))
	_, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadySuperseded))
	assert.Empty(t, scans.created, "no scan row when prelude rejects")
}

func TestStart_AlreadyExecuted(t *testing.T) {
	t.Parallel()
	uc, dec, gr, scans, _ := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, true) // with GUID "g-orig"
	require.NoError(t, gr.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 1, SeasonNumber: 1,
		ReleaseGUID: "g-orig", Status: grab.StatusGrabbed,
		ScanRunID: original.ScanRunID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	_, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExecuted))
	assert.Empty(t, scans.created)
}

func TestStart_NotFound(t *testing.T) {
	t.Parallel()
	uc, _, _, scans, _ := newUC(t, &rescanFakeSonarr{})
	_, err := uc.Start(context.Background(), Input{DecisionID: uuid.New()})
	require.True(t, errors.Is(err, ports.ErrNotFound))
	assert.Empty(t, scans.created)
}

func TestStart_UnknownInstance(t *testing.T) {
	t.Parallel()
	uc, dec, _, scans, _ := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, false)
	original.InstanceName = "ghost"
	require.NoError(t, dec.Save(context.Background(), original))
	_, err := uc.Start(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown instance")
	assert.Empty(t, scans.created)
}
