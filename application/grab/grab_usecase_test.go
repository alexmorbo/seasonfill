package grab

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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	domaingrab "github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/release"
	"github.com/alexmorbo/seasonfill/domain/series"
)

type fakeClassifier struct {
	transient func(error) bool
	is4xx     func(error) bool
}

func (f fakeClassifier) IsTransient(err error) bool {
	if f.transient == nil {
		return false
	}
	return f.transient(err)
}
func (f fakeClassifier) Is4xx(err error) bool {
	if f.is4xx == nil {
		return false
	}
	return f.is4xx(err)
}

type fakeSonarrGrab struct {
	mu       sync.Mutex
	calls    int
	errors   []error
	gotGUID  string
	gotIdxID int
}

func (f *fakeSonarrGrab) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (f *fakeSonarrGrab) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarrGrab) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarrGrab) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) SearchReleases(_ context.Context, _, _ int) ([]release.Release, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) GetQualityProfile(_ context.Context, _ int) (ports.QualityProfile, error) {
	return ports.QualityProfile{}, nil
}
func (f *fakeSonarrGrab) ListIndexers(_ context.Context) ([]ports.Indexer, error) { return nil, nil }
func (f *fakeSonarrGrab) ListTags(_ context.Context) ([]ports.Tag, error)         { return nil, nil }
func (f *fakeSonarrGrab) GrabHistory(_ context.Context, _ int) ([]ports.HistoryEvent, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) ForceGrab(_ context.Context, guid string, indexerID int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotGUID = guid
	f.gotIdxID = indexerID
	idx := f.calls
	f.calls++
	if idx >= len(f.errors) {
		return nil
	}
	return f.errors[idx]
}
func (f *fakeSonarrGrab) Name() string { return "fake" }

type fakeGrabRepo struct {
	mu   sync.Mutex
	recs []domaingrab.Record
	err  error
}

func (r *fakeGrabRepo) Create(_ context.Context, rec domaingrab.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.recs = append(r.recs, rec)
	return nil
}

func (r *fakeGrabRepo) List(_ context.Context, _ ports.GrabFilter, _ ports.Pagination) ([]domaingrab.Record, *ports.Cursor, error) {
	panic("fake List unexpectedly called - this stub is not configured for List queries")
}

type fakeCooldownRepo struct {
	mu sync.Mutex
	cs []cooldown.Cooldown
}

func (r *fakeCooldownRepo) Set(_ context.Context, c cooldown.Cooldown) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cs = append(r.cs, c)
	return nil
}
func (r *fakeCooldownRepo) Get(_ context.Context, _ cooldown.Scope, _ string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (r *fakeCooldownRepo) FilterActive(_ context.Context, _ cooldown.Scope, _ []string, _ time.Time) ([]cooldown.Cooldown, error) {
	return nil, nil
}
func (r *fakeCooldownRepo) Sweep(_ context.Context, _ time.Time) (int64, error) { return 0, nil }

type fakeOriginRepo struct {
	mu  sync.Mutex
	ups []ports.OriginRelease
}

func (r *fakeOriginRepo) Get(_ context.Context, _ string, _, _ int) (ports.OriginRelease, bool, error) {
	return ports.OriginRelease{}, false, nil
}
func (r *fakeOriginRepo) Upsert(_ context.Context, rec ports.OriginRelease) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ups = append(r.ups, rec)
	return nil
}

func newInput(s *fakeSonarrGrab) Input {
	return Input{
		ScanRunID:    uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		Selected: release.Scored{
			Release: release.Release{
				GUID:        "g1",
				Title:       "Pack",
				IndexerID:   3,
				IndexerName: "RT",
			},
			Coverage: 5,
		},
		Coverage: 5,
		Sonarr:   s,
		Config: Config{
			MaxAttempts:    3,
			InitialBackoff: time.Millisecond,
			MaxBackoff:     time.Millisecond,
			SeriesCooldown: 24 * time.Hour,
			GUIDCooldown:   72 * time.Hour,
		},
	}
}

func noopSleep(_ context.Context, _ time.Duration) error { return nil }

func newUC(t *testing.T) (*UseCase, *fakeGrabRepo, *fakeCooldownRepo, *fakeOriginRepo) {
	t.Helper()
	gr := &fakeGrabRepo{}
	cr := &fakeCooldownRepo{}
	or := &fakeOriginRepo{}
	uc := NewUseCase(gr, cr, or,
		fakeClassifier{
			transient: func(e error) bool { return errors.Is(e, errTransient) },
			is4xx:     func(e error) bool { return errors.Is(e, err4xx) },
		},
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	).WithSleeper(noopSleep)
	return uc, gr, cr, or
}

var (
	errTransient = errors.New("transient")
	err4xx       = errors.New("4xx")
)

func TestExecute_Success_FirstAttempt(t *testing.T) {
	t.Parallel()
	uc, gr, cr, or := newUC(t)
	sonarr := &fakeSonarrGrab{}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	assert.Equal(t, 1, out.Record.Attempts)
	assert.Equal(t, "g1", sonarr.gotGUID)
	assert.Equal(t, 3, sonarr.gotIdxID)
	require.Len(t, gr.recs, 1)
	require.Len(t, cr.cs, 1)
	assert.Equal(t, cooldown.ScopeSeries, cr.cs[0].Scope)
	require.Len(t, or.ups, 1)
	assert.Equal(t, "our_grab", or.ups[0].Source)
}

func TestExecute_TransientThenSuccess(t *testing.T) {
	t.Parallel()
	uc, gr, cr, _ := newUC(t)
	sonarr := &fakeSonarrGrab{errors: []error{errTransient, errTransient, nil}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	assert.Equal(t, 3, sonarr.calls)
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	require.Len(t, gr.recs, 1)
	// Series cooldown set; no guid cooldown.
	require.Len(t, cr.cs, 1)
	assert.Equal(t, cooldown.ScopeSeries, cr.cs[0].Scope)
}

func TestExecute_TransientExhausted(t *testing.T) {
	t.Parallel()
	uc, gr, cr, _ := newUC(t)
	sonarr := &fakeSonarrGrab{errors: []error{errTransient, errTransient, errTransient}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err)
	assert.True(t, IsGrabFailed(out.Err))
	assert.True(t, errors.Is(out.Err, domain.ErrGrabFailed))
	assert.Equal(t, 3, sonarr.calls)
	assert.Equal(t, domaingrab.StatusGrabFailed, out.Record.Status)
	require.Len(t, gr.recs, 1)
	require.Len(t, cr.cs, 1)
	assert.Equal(t, cooldown.ScopeGUID, cr.cs[0].Scope)
}

func TestExecute_4xxNoRetry(t *testing.T) {
	t.Parallel()
	uc, gr, cr, _ := newUC(t)
	sonarr := &fakeSonarrGrab{errors: []error{err4xx}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err)
	assert.Equal(t, 1, sonarr.calls, "must not retry on 4xx")
	assert.Equal(t, domaingrab.StatusGrabFailed, out.Record.Status)
	require.Len(t, gr.recs, 1)
	require.Len(t, cr.cs, 1)
	assert.Equal(t, cooldown.ScopeGUID, cr.cs[0].Scope)
}

func TestExecute_UnclassifiedNoRetry(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := newUC(t)
	sonarr := &fakeSonarrGrab{errors: []error{errors.New("???")}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err)
	assert.Equal(t, 1, sonarr.calls, "must not retry on unclassified")
}

func TestExecute_ContextCancelDuringBackoff(t *testing.T) {
	t.Parallel()
	uc, _, _, _ := newUC(t)
	uc.WithSleeper(func(_ context.Context, _ time.Duration) error {
		return context.Canceled
	})
	sonarr := &fakeSonarrGrab{errors: []error{errTransient, errTransient, nil}}
	out := uc.Execute(context.Background(), newInput(sonarr))
	require.Error(t, out.Err)
	assert.True(t, IsGrabFailed(out.Err))
	// One call, then cancel during backoff → no second call.
	assert.Equal(t, 1, sonarr.calls)
}

func TestDefaultSleeper_RespectsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DefaultSleeper(ctx, 50*time.Millisecond)
	require.Error(t, err)
}

func TestDefaultSleeper_ZeroDuration(t *testing.T) {
	t.Parallel()
	require.NoError(t, DefaultSleeper(context.Background(), 0))
}

func TestDefaultSleeper_Completes(t *testing.T) {
	t.Parallel()
	start := time.Now()
	require.NoError(t, DefaultSleeper(context.Background(), 10*time.Millisecond))
	assert.GreaterOrEqual(t, time.Since(start), 10*time.Millisecond)
}
