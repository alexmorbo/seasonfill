package rescan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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

func newUC(t *testing.T, sn *rescanFakeSonarr) (*UseCase, *rescanFakeDec, *rescanFakeGrab) {
	t.Helper()
	lg := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dec, gr := &rescanFakeDec{}, &rescanFakeGrab{}
	ev := evaluate.NewUseCase(sn, dec, lg)
	inst := scan.Instance{Config: config.SonarrInstance{Name: "alpha"}, Client: sn}
	return NewUseCase(dec, gr, ev, map[string]scan.Instance{"alpha": inst}, lg), dec, gr
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

func TestExecute_HappyPath_PicksNewReleaseAndSupersedes(t *testing.T) {
	sn := &rescanFakeSonarr{releases: []release.Release{
		{GUID: "g-new", Title: "rescan-pick", QualityID: 19, Seeders: 100, SizeBytes: 1e9},
	}}
	uc, dec, _ := newUC(t, sn)
	original := seedOriginal(t, dec, false)
	out, err := uc.Execute(context.Background(), Input{DecisionID: original.ID})
	require.NoError(t, err)
	assert.NotEqual(t, original.ID, out.NewDecision.ID)
	assert.Equal(t, original.ScanRunID, out.NewDecision.ScanRunID,
		"017 §3.4: new decision shares scan_run_id")
	loaded, _ := dec.GetByID(context.Background(), original.ID)
	require.NotNil(t, loaded.SupersededByID)
	assert.Equal(t, out.NewDecision.ID, *loaded.SupersededByID)
}

func TestExecute_AlreadySuperseded(t *testing.T) {
	uc, dec, _ := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, false)
	require.NoError(t, dec.UpdateSupersededBy(context.Background(), original.ID, uuid.New()))
	_, err := uc.Execute(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadySuperseded))
}

func TestExecute_AlreadyExecuted(t *testing.T) {
	uc, dec, gr := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, true) // with GUID "g-orig"
	require.NoError(t, gr.Create(context.Background(), grab.Record{
		ID: uuid.New(), InstanceName: "alpha", SeriesID: 1, SeasonNumber: 1,
		ReleaseGUID: "g-orig", Status: grab.StatusGrabbed,
		ScanRunID: original.ScanRunID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}))
	_, err := uc.Execute(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyExecuted))
}

func TestExecute_NotFound(t *testing.T) {
	uc, _, _ := newUC(t, &rescanFakeSonarr{})
	_, err := uc.Execute(context.Background(), Input{DecisionID: uuid.New()})
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestExecute_UnknownInstance(t *testing.T) {
	uc, dec, _ := newUC(t, &rescanFakeSonarr{})
	original := seedOriginal(t, dec, false)
	original.InstanceName = "ghost"
	require.NoError(t, dec.Save(context.Background(), original))
	_, err := uc.Execute(context.Background(), Input{DecisionID: original.ID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown instance")
}
