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
	mu          sync.Mutex
	calls       int
	errors      []error
	downloadIDs []string // optional, indexed by call number; "" when nil/short
	gotGUID     string
	gotIdxID    int
}

func (f *fakeSonarrGrab) SystemStatus(_ context.Context) (ports.SystemStatus, error) {
	return ports.SystemStatus{}, nil
}
func (f *fakeSonarrGrab) ListSeries(_ context.Context) ([]series.Series, error) { return nil, nil }
func (f *fakeSonarrGrab) ListSeriesCache(_ context.Context, _ string) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) GetSeries(_ context.Context, _ int) (series.Series, error) {
	return series.Series{}, nil
}
func (f *fakeSonarrGrab) ListEpisodes(_ context.Context, _, _ int) ([]series.Episode, error) {
	return nil, nil
}

func (f *fakeSonarrGrab) ListEpisodesBySeries(_ context.Context, _ int) ([]series.Episode, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) ListEpisodeFiles(_ context.Context, _ int) (map[int]int, error) {
	return nil, nil
}
func (f *fakeSonarrGrab) ListEpisodeFilesBySeason(_ context.Context, _, _ int) ([]ports.EpisodeFileDetail, error) {
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
func (f *fakeSonarrGrab) ParseRelease(_ context.Context, _ string) (ports.ParseResult, error) {
	return ports.ParseResult{}, nil
}
func (f *fakeSonarrGrab) ForceGrab(_ context.Context, guid string, indexerID int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotGUID = guid
	f.gotIdxID = indexerID
	idx := f.calls
	f.calls++
	var dlID string
	if idx < len(f.downloadIDs) {
		dlID = f.downloadIDs[idx]
	}
	if idx >= len(f.errors) {
		return dlID, nil
	}
	return "", f.errors[idx]
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

func (r *fakeGrabRepo) MatchLatest(_ context.Context, _ ports.MatchKey) (domaingrab.Record, error) {
	panic("fake MatchLatest unexpectedly called - this stub is not configured for MatchLatest queries")
}

func (r *fakeGrabRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ domaingrab.Status, _ string) error {
	panic("fake UpdateStatus unexpectedly called - this stub is not configured for UpdateStatus calls")
}

func (r *fakeGrabRepo) UpdateTorrentHash(_ context.Context, _ uuid.UUID, _ string) error {
	panic("fake UpdateTorrentHash unexpectedly called - this stub is not configured for UpdateTorrentHash calls")
}

func (r *fakeGrabRepo) FindLatestSuccessByHash(_ context.Context, _ string) (domaingrab.Record, error) {
	panic("fake FindLatestSuccessByHash unexpectedly called - this stub is not configured")
}

func (r *fakeGrabRepo) CreateReplay(_ context.Context, rec domaingrab.Record, _ uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.recs = append(r.recs, rec)
	return nil
}

func (r *fakeGrabRepo) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}

func (r *fakeGrabRepo) ListReplaysOf(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}

func (r *fakeGrabRepo) UpdateSizeBytes(_ context.Context, _ uuid.UUID, _ int64) error {
	return nil
}

func (r *fakeGrabRepo) GetByID(_ context.Context, _ uuid.UUID) (domaingrab.Record, error) {
	return domaingrab.Record{}, ports.ErrNotFound
}

func (r *fakeGrabRepo) CountReplaysSince(_ context.Context, _ string, _ time.Time) (int, error) {
	return 0, nil
}

func (r *fakeGrabRepo) CountReplaysAll(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (r *fakeGrabRepo) CountImportedEpisodes(_ context.Context, _ string, _, _ int) (int, error) {
	return 0, nil
}
func (r *fakeGrabRepo) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]domaingrab.Record, error) {
	return nil, nil
}
func (r *fakeGrabRepo) UpdateParsed(_ context.Context, _ uuid.UUID, _ *domaingrab.Parsed, _ time.Time) error {
	return nil
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

func TestExecute_Success_PopulatesTorrentHash_Valid40HexLower(t *testing.T) {
	t.Parallel()
	uc, gr, _, _ := newUC(t)
	const hash = "0123456789abcdef0123456789abcdef01234567"
	sonarr := &fakeSonarrGrab{downloadIDs: []string{hash}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	require.NotNil(t, out.Record.TorrentHash)
	assert.Equal(t, hash, *out.Record.TorrentHash)
	require.Len(t, gr.recs, 1)
	require.NotNil(t, gr.recs[0].TorrentHash)
	assert.Equal(t, hash, *gr.recs[0].TorrentHash,
		"persisted row must carry the parsed lowercase 40-char hex hash")
}

func TestExecute_Success_TorrentHashNormalisesUpperToLower(t *testing.T) {
	t.Parallel()
	uc, gr, _, _ := newUC(t)
	const upper = "0123456789ABCDEF0123456789ABCDEF01234567"
	const lower = "0123456789abcdef0123456789abcdef01234567"
	sonarr := &fakeSonarrGrab{downloadIDs: []string{upper}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	require.NotNil(t, out.Record.TorrentHash)
	assert.Equal(t, lower, *out.Record.TorrentHash,
		"Sonarr-returned hash must be lowercased before persist")
	require.Len(t, gr.recs, 1)
	require.NotNil(t, gr.recs[0].TorrentHash)
	assert.Equal(t, lower, *gr.recs[0].TorrentHash)
}

func TestExecute_Success_TorrentHashNilOnMalformed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		dlID string
	}{
		{"empty downloadId (no qBit / Sonarr omitted field)", ""},
		{"too short (16-char legacy id)", "ABCDEF1234567890"},
		{"too long (41 chars)", "0123456789abcdef0123456789abcdef012345678"},
		{"non-hex characters", "GGGG456789abcdef0123456789abcdef01234567"},
		{"hyphenated guid form", "0123-4567-89ab-cdef-0123-4567-89ab-cdef-01"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc, gr, _, _ := newUC(t)
			sonarr := &fakeSonarrGrab{downloadIDs: []string{tc.dlID}}
			out := uc.Execute(context.Background(), newInput(sonarr))

			require.NoError(t, out.Err)
			assert.Equal(t, tc.dlID, out.Record.DownloadID,
				"DownloadID stays as-is — only TorrentHash gets the strict-hex filter")
			assert.Nil(t, out.Record.TorrentHash,
				"malformed downloadId must leave TorrentHash nil (D63 — no half-validated hashes)")
			require.Len(t, gr.recs, 1)
			assert.Nil(t, gr.recs[0].TorrentHash)
		})
	}
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

func TestExecute_Success_PopulatesDownloadID(t *testing.T) {
	t.Parallel()
	uc, gr, _, _ := newUC(t)
	sonarr := &fakeSonarrGrab{downloadIDs: []string{"DL-42"}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	assert.Equal(t, "DL-42", out.Record.DownloadID, "Record must carry the Sonarr-returned downloadID")
	require.Len(t, gr.recs, 1)
	assert.Equal(t, "DL-42", gr.recs[0].DownloadID, "persisted row must carry the same downloadID")
}

func TestExecute_Success_EmptyDownloadIDPersists(t *testing.T) {
	t.Parallel()
	uc, gr, _, _ := newUC(t)
	// fakeSonarrGrab with no downloadIDs slice → ForceGrab returns "" — the
	// realistic steady-state when Sonarr's response omits downloadClientId.
	sonarr := &fakeSonarrGrab{}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	assert.Equal(t, "", out.Record.DownloadID, "empty downloadID is the legitimate steady-state, not an error")
	require.Len(t, gr.recs, 1)
	assert.Equal(t, "", gr.recs[0].DownloadID)
}

// TestExecute_WithClock_CooldownExpiresAtIsDeterministic — item #5.
// Fixed clock proves cooldown ExpiresAt and CreatedAt math are
// predictable to the nanosecond after the six time.Now() → u.now()
// conversion.
func TestExecute_WithClock_CooldownExpiresAtIsDeterministic(t *testing.T) {
	t.Parallel()
	fixedNow := time.Date(2026, 5, 22, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	t.Run("success path series cooldown", func(t *testing.T) {
		t.Parallel()
		uc, _, cr, _ := newUC(t)
		uc.WithClock(clock)
		in := newInput(&fakeSonarrGrab{})
		out := uc.Execute(context.Background(), in)
		require.NoError(t, out.Err)
		require.Len(t, cr.cs, 1)
		assert.Equal(t, cooldown.ScopeSeries, cr.cs[0].Scope)
		assert.Equal(t, fixedNow.Add(in.Config.SeriesCooldown), cr.cs[0].ExpiresAt)
		assert.Equal(t, fixedNow, cr.cs[0].CreatedAt)
	})

	t.Run("failed path guid cooldown", func(t *testing.T) {
		t.Parallel()
		uc, _, cr, _ := newUC(t)
		uc.WithClock(clock)
		in := newInput(&fakeSonarrGrab{errors: []error{err4xx}})
		out := uc.Execute(context.Background(), in)
		require.Error(t, out.Err)
		require.Len(t, cr.cs, 1)
		assert.Equal(t, cooldown.ScopeGUID, cr.cs[0].Scope)
		assert.Equal(t, fixedNow.Add(in.Config.GUIDCooldown), cr.cs[0].ExpiresAt)
		assert.Equal(t, fixedNow, cr.cs[0].CreatedAt)
	})

	t.Run("record CreatedAt and UpdatedAt", func(t *testing.T) {
		t.Parallel()
		uc, gr, _, _ := newUC(t)
		uc.WithClock(clock)
		out := uc.Execute(context.Background(), newInput(&fakeSonarrGrab{}))
		require.NoError(t, out.Err)
		require.Len(t, gr.recs, 1)
		assert.Equal(t, fixedNow, gr.recs[0].CreatedAt)
		assert.Equal(t, fixedNow, gr.recs[0].UpdatedAt)
	})
}

func TestExecute_Success_PersistFails_BubblesError(t *testing.T) {
	t.Parallel()
	uc, gr, _, _ := newUC(t)
	gr.err = errors.New("disk full")
	sonarr := &fakeSonarrGrab{}

	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err, "persist failure must surface as Output.Err")
	assert.Contains(t, out.Err.Error(), "persist grab success")
	assert.Contains(t, out.Err.Error(), "disk full")
	// Sonarr was successful, so this is NOT the ErrGrabFailed sentinel.
	assert.False(t, IsGrabFailed(out.Err),
		"persist failure is distinct from Sonarr-side grab failure")
	// Record still describes the grab attempt; status was set to grabbed
	// before persistSuccess ran.
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	assert.Equal(t, 1, out.Attempts)
	// The fakeGrabRepo's Create returned err before appending — no row.
	assert.Empty(t, gr.recs, "no row was persisted")
}

func TestExecute_Success_PersistsSizeBytes_FromRelease(t *testing.T) {
	t.Parallel()
	sonarr := &fakeSonarrGrab{downloadIDs: []string{"abcdef0123456789abcdef0123456789abcdef01"}}
	uc, gr, _, _ := newUC(t)
	in := newInput(sonarr)
	in.Selected.Release.SizeBytes = 13_325_829_734
	out := uc.Execute(context.Background(), in)
	require.NoError(t, out.Err)
	require.Len(t, gr.recs, 1)
	require.NotNil(t, gr.recs[0].SizeBytes)
	require.Equal(t, int64(13_325_829_734), *gr.recs[0].SizeBytes)
}

func TestExecute_Success_SizeBytesNilWhenReleaseSizeZero(t *testing.T) {
	t.Parallel()
	sonarr := &fakeSonarrGrab{downloadIDs: []string{"abcdef0123456789abcdef0123456789abcdef01"}}
	uc, gr, _, _ := newUC(t)
	in := newInput(sonarr)
	in.Selected.Release.SizeBytes = 0
	out := uc.Execute(context.Background(), in)
	require.NoError(t, out.Err)
	require.Len(t, gr.recs, 1)
	require.Nil(t, gr.recs[0].SizeBytes, "0-byte payload must persist as NULL")
}
