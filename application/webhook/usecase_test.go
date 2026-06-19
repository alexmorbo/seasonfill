package webhook

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

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	domainwebhook "github.com/alexmorbo/seasonfill/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeGrabRepo struct {
	mu              sync.Mutex
	match           grab.Record
	matchErr        error
	matchKey        ports.MatchKey
	matchCalls      int
	updateErr       error
	updateID        uuid.UUID
	updateStatus    grab.Status
	updateMessage   string
	updateCalls     int
	hashUpdateErr   error
	hashUpdateID    uuid.UUID
	hashUpdateVal   string
	hashUpdateCall  int
	lastUpdatedSize int64
}

func (r *fakeGrabRepo) Create(context.Context, grab.Record) error {
	return errors.New("create not used in 007b tests")
}
func (r *fakeGrabRepo) List(context.Context, ports.GrabFilter, ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	return nil, nil, errors.New("list not used in 007b tests")
}
func (r *fakeGrabRepo) MatchLatest(_ context.Context, k ports.MatchKey) (grab.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.matchKey = k
	r.matchCalls++
	if r.matchErr != nil {
		return grab.Record{}, r.matchErr
	}
	return r.match, nil
}
func (r *fakeGrabRepo) UpdateStatus(_ context.Context, id uuid.UUID, s grab.Status, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateID = id
	r.updateStatus = s
	r.updateMessage = msg
	r.updateCalls++
	return r.updateErr
}

func (r *fakeGrabRepo) UpdateTorrentHash(_ context.Context, id uuid.UUID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hashUpdateID = id
	r.hashUpdateVal = hash
	r.hashUpdateCall++
	return r.hashUpdateErr
}

func (r *fakeGrabRepo) FindLatestSuccessByHash(_ context.Context, _ string) (grab.Record, error) {
	panic("fake FindLatestSuccessByHash unexpectedly called - this stub is not configured")
}

func (r *fakeGrabRepo) CreateReplay(_ context.Context, rec grab.Record, _ uuid.UUID) error {
	panic("fake CreateReplay unexpectedly called - this stub is not configured")
}

func (r *fakeGrabRepo) SetReplayOfID(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	panic("fake SetReplayOfID unexpectedly called - this stub is not configured")
}

func (r *fakeGrabRepo) ListReplaysOf(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return map[uuid.UUID][]uuid.UUID{}, nil
}

func (r *fakeGrabRepo) UpdateSizeBytes(_ context.Context, _ uuid.UUID, size int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	r.lastUpdatedSize = size
	return nil
}

func (r *fakeGrabRepo) GetByID(_ context.Context, _ uuid.UUID) (grab.Record, error) {
	return grab.Record{}, ports.ErrNotFound
}

func (r *fakeGrabRepo) CountReplaysSince(_ context.Context, _ domain.InstanceName, _ time.Time) (int, error) {
	return 0, nil
}

func (r *fakeGrabRepo) CountReplaysAll(_ context.Context, _ domain.InstanceName) (int, error) {
	return 0, nil
}

func (r *fakeGrabRepo) CountImportedEpisodes(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) (int, error) {
	return 0, nil
}

func (r *fakeGrabRepo) ListUnparsedSince(_ context.Context, _ time.Time, _ int) ([]grab.Record, error) {
	return nil, nil
}

func (r *fakeGrabRepo) UpdateParsed(_ context.Context, _ uuid.UUID, _ *grab.Parsed, _ time.Time) error {
	return nil
}

type fakeCooldownRepo struct {
	mu     sync.Mutex
	sets   []cooldown.Cooldown
	setErr error
}

func (r *fakeCooldownRepo) Set(_ context.Context, c cooldown.Cooldown) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.setErr != nil {
		return r.setErr
	}
	r.sets = append(r.sets, c)
	return nil
}
func (r *fakeCooldownRepo) Get(context.Context, cooldown.Scope, string) (cooldown.Cooldown, bool, error) {
	return cooldown.Cooldown{}, false, nil
}
func (r *fakeCooldownRepo) FilterActive(context.Context, cooldown.Scope, []string, time.Time) ([]cooldown.Cooldown, error) {
	return nil, nil
}
func (r *fakeCooldownRepo) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

type fakeTransactor struct {
	mu        sync.Mutex
	committed bool
	called    int
}

func (t *fakeTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.mu.Lock()
	t.called++
	t.mu.Unlock()
	if err := fn(ctx); err != nil {
		return err
	}
	t.mu.Lock()
	t.committed = true
	t.mu.Unlock()
	return nil
}

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func sampleRecord() grab.Record {
	return grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		ReleaseGUID:  "g1",
		ReleaseTitle: "Hijack.S02.PACK",
		DownloadID:   "DL-1",
		Status:       grab.StatusGrabbed,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

// fixedLookup maps "main" → 48h, other names → 0.
func fixedLookup() GuidCooldownLookup {
	return func(instance domain.InstanceName) time.Duration {
		if instance == "main" {
			return 48 * time.Hour
		}
		return 0
	}
}

func newUseCase(t *testing.T, g *fakeGrabRepo, c *fakeCooldownRepo, tx *fakeTransactor) *UseCase {
	t.Helper()
	return New(Deps{
		Grabs:              g,
		Cooldowns:          c,
		Tx:                 tx,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})
}

type fakeSeriesCache struct {
	mu            sync.Mutex
	upsertCalls   int
	upsertedEntry series.CacheEntry
	upsertErr     error
	deleteCalls   int
	deletedID     domain.SonarrSeriesID
	deletedInst   domain.InstanceName
	deleteErr     error
}

func (f *fakeSeriesCache) Get(context.Context, domain.InstanceName, domain.SonarrSeriesID) (series.CacheEntry, error) {
	return series.CacheEntry{}, ports.ErrNotFound
}
func (f *fakeSeriesCache) Upsert(_ context.Context, e series.CacheEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upsertedEntry = e
	return nil
}
func (f *fakeSeriesCache) SoftDelete(_ context.Context, instance domain.InstanceName, id domain.SonarrSeriesID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedInst = instance
	f.deletedID = id
	return nil
}
func (f *fakeSeriesCache) ListActiveByInstance(context.Context, domain.InstanceName) ([]series.CacheEntry, error) {
	return nil, nil
}
func (f *fakeSeriesCache) ListByFilter(_ context.Context, _ domain.InstanceName, _ ports.SeriesCacheFilter, _ ports.SeriesCacheSort, _ ports.Pagination) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	return nil, 0, false, nil, nil
}
func (f *fakeSeriesCache) FetchLastGrabInfo(_ context.Context, _ domain.InstanceName, _ []domain.SonarrSeriesID) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	return make(map[domain.SonarrSeriesID]ports.LastGrabInfo), nil
}
func (f *fakeSeriesCache) ListDistinctNetworks(_ context.Context, _ domain.InstanceName) ([]string, error) {
	return nil, nil
}

var _ ports.SeriesCacheRepository = (*fakeSeriesCache)(nil)

func newUseCaseWithCache(t *testing.T, g *fakeGrabRepo, c *fakeCooldownRepo, tx *fakeTransactor, cache *fakeSeriesCache) *UseCase {
	t.Helper()
	return New(Deps{
		Grabs:              g,
		Cooldowns:          c,
		SeriesCache:        cache,
		Tx:                 tx,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})
}

func TestProcess_Imported_HappyPath_NoCooldown(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImported, InstanceName: "main",
		DownloadID: rec.DownloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.updateCalls)
	assert.Equal(t, grab.StatusImported, g.updateStatus)
	assert.Equal(t, rec.ID, g.updateID)
	assert.Empty(t, c.sets)
	assert.True(t, tx.committed)
}

func TestProcess_ImportFailed_AddsGuidCooldown(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)
	occurred := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID: rec.DownloadID, Message: "missing audio",
		OccurredAt: occurred,
	})
	require.NoError(t, err)
	require.Equal(t, 1, g.updateCalls)
	assert.Equal(t, grab.StatusImportFailed, g.updateStatus)
	assert.Equal(t, "missing audio", g.updateMessage)
	require.Len(t, c.sets, 1)
	cd := c.sets[0]
	assert.Equal(t, cooldown.ScopeGUID, cd.Scope)
	assert.Equal(t, rec.ReleaseGUID, cd.Key)
	assert.Equal(t, "guid_after_failed_import", cd.Reason)
	assert.Equal(t, occurred.Add(48*time.Hour), cd.ExpiresAt)
	assert.True(t, tx.committed)
}

func TestProcess_OrphanEvent_NoWrite_NoError(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{matchErr: ports.ErrNotFound}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImported, InstanceName: "main", DownloadID: "ghost",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.matchCalls)
	assert.Equal(t, 0, g.updateCalls)
	assert.Empty(t, c.sets)
	assert.Equal(t, 0, tx.called)
}

func TestProcess_AlreadyTerminal_NoOp(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.Status = grab.StatusImported
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImported, InstanceName: "main", DownloadID: rec.DownloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.matchCalls)
	assert.Equal(t, 0, g.updateCalls)
	assert.Empty(t, c.sets)
	assert.Equal(t, 0, tx.called)
}

func TestProcess_TransactorRollsBack_OnCooldownFailure(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{setErr: errors.New("cooldown write boom")}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main", DownloadID: rec.DownloadID,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrDBUnavailable,
		"transactor errors must be wrapped with ports.ErrDBUnavailable "+
			"so the webhook handler classifies them as transient (007c)")
	assert.False(t, tx.committed)
	assert.Equal(t, 1, g.updateCalls)
	assert.Empty(t, c.sets)
}

func TestProcess_DownloadIDPrecedence_KeyPassedThrough(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImported, InstanceName: "main",
		DownloadID: "PRIMARY-DL", ReleaseTitle: "FALLBACK.TITLE",
		SeriesID: 999, SeasonNumber: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, ports.MatchKey{
		DownloadID: "PRIMARY-DL", ReleaseTitle: "FALLBACK.TITLE",
		SeriesID: 999, SeasonNumber: 1, InstanceName: "main",
	}, g.matchKey)
	assert.Equal(t, 1, g.matchCalls)
}

func TestProcess_Unsupported_NoCalls(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, c, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeUnsupported, InstanceName: "main",
		RawEventType: "Rename",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, g.matchCalls)
	assert.Equal(t, 0, g.hashUpdateCall)
	assert.Empty(t, c.sets)
	assert.Equal(t, 0, tx.called)
}

func TestProcess_Grabbed_MalformedHash_NoCalls(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	for _, dlID := range []string{"", "DL-too-short", "ABCDEF1234567890"} {
		err := uc.Process(context.Background(), domainwebhook.Event{
			Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
			DownloadID: dlID,
		})
		require.NoError(t, err, "downloadId=%q", dlID)
	}
	assert.Equal(t, 0, g.matchCalls, "malformed downloadId must never trigger a DB lookup")
	assert.Equal(t, 0, g.hashUpdateCall)
}

func TestProcess_MatchError_PropagatesNonNotFound(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{matchErr: errors.New("db down")}
	tx := &fakeTransactor{}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, tx)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImported, InstanceName: "main", DownloadID: "DL-1",
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ports.ErrNotFound)
	assert.Equal(t, 0, tx.called)
}

func TestProcess_NilTransactor_DirectWrites(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	uc := New(Deps{
		Grabs: g, Cooldowns: c, Tx: nil,
		GUIDCooldownLookup: fixedLookup(), Logger: quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main", DownloadID: rec.DownloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.updateCalls)
	assert.Len(t, c.sets, 1)
}

func TestProcess_ImportFailed_ZeroCooldown_NoCooldownAdded(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := New(Deps{
		Grabs: g, Cooldowns: c, Tx: tx,
		GUIDCooldownLookup: func(domain.InstanceName) time.Duration { return 0 },
		Logger:             quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main", DownloadID: rec.DownloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.updateCalls)
	assert.Empty(t, c.sets)
	assert.True(t, tx.committed)
}

// TestProcess_ImportFailed_UnknownInstance_NoCooldownButTransitionApplies —
// item #4. Webhook for "rogue" (not in our config). Lookup returns 0.
// Status row still flips; no cooldown row written.
func TestProcess_ImportFailed_UnknownInstance_NoCooldownButTransitionApplies(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.InstanceName = "rogue"
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := New(Deps{
		Grabs: g, Cooldowns: c, Tx: tx,
		GUIDCooldownLookup: fixedLookup(), Logger: quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "rogue",
		DownloadID: rec.DownloadID, Message: "missing audio",
		OccurredAt: time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, 1, g.updateCalls, "status must transition even when instance unknown")
	assert.Equal(t, grab.StatusImportFailed, g.updateStatus)
	assert.Empty(t, c.sets, "unknown instance must NOT write a cooldown row")
	assert.True(t, tx.committed)
}

// TestProcess_ImportFailed_PerInstanceLookup_DurationFromClosure — item #4.
// Two instances ("a" → 24h, "b" → 72h) produce cooldown rows with the
// correct per-instance ExpiresAt.
func TestProcess_ImportFailed_PerInstanceLookup_DurationFromClosure(t *testing.T) {
	t.Parallel()
	lookup := func(name domain.InstanceName) time.Duration {
		switch name {
		case "a":
			return 24 * time.Hour
		case "b":
			return 72 * time.Hour
		default:
			return 0
		}
	}
	occurred := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		instance domain.InstanceName
		wantDur  time.Duration
	}{
		{"a", 24 * time.Hour},
		{"b", 72 * time.Hour},
	} {
		tc := tc
		t.Run(string(tc.instance), func(t *testing.T) {
			t.Parallel()
			rec := sampleRecord()
			rec.InstanceName = tc.instance
			g := &fakeGrabRepo{match: rec}
			c := &fakeCooldownRepo{}
			tx := &fakeTransactor{}
			uc := New(Deps{
				Grabs: g, Cooldowns: c, Tx: tx,
				GUIDCooldownLookup: lookup, Logger: quietLogger(),
			})

			err := uc.Process(context.Background(), domainwebhook.Event{
				Type: domainwebhook.EventTypeImportFailed, InstanceName: tc.instance,
				DownloadID: rec.DownloadID, OccurredAt: occurred,
			})
			require.NoError(t, err)
			require.Len(t, c.sets, 1)
			assert.Equal(t, occurred.Add(tc.wantDur), c.sets[0].ExpiresAt,
				"ExpiresAt must use the per-instance cooldown from the lookup")
		})
	}
}

// TestProcess_ImportFailed_LookupReadsLiveAfterPointerSwap — 032e.
// Proves the cooldown lookup is evaluated per-invocation rather than
// captured at UC-construction time. Mirrors the runtime holder shape:
// the closure reads from an atomic.Pointer the test swaps between two
// calls. If the closure ever caches its result, the second cooldown
// row will keep the stale 24h duration and the assertion fails.
func TestProcess_ImportFailed_LookupReadsLiveAfterPointerSwap(t *testing.T) {
	t.Parallel()

	var ptr atomic.Pointer[time.Duration]
	first := 24 * time.Hour
	ptr.Store(&first)

	lookup := func(name domain.InstanceName) time.Duration {
		if name != "main" {
			return 0
		}
		if d := ptr.Load(); d != nil {
			return *d
		}
		return 0
	}

	rec1 := sampleRecord()
	g1 := &fakeGrabRepo{match: rec1}
	c1 := &fakeCooldownRepo{}
	tx1 := &fakeTransactor{}
	uc := New(Deps{
		Grabs: g1, Cooldowns: c1, Tx: tx1,
		GUIDCooldownLookup: lookup, Logger: quietLogger(),
	})

	occurred := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID: rec1.DownloadID, OccurredAt: occurred,
	})
	require.NoError(t, err)
	require.Len(t, c1.sets, 1)
	assert.Equal(t, occurred.Add(24*time.Hour), c1.sets[0].ExpiresAt,
		"first call must observe the pre-swap duration")

	// Simulate a runtime reload swapping in a new cooldown.
	second := 96 * time.Hour
	ptr.Store(&second)

	rec2 := sampleRecord()
	g2 := &fakeGrabRepo{match: rec2}
	c2 := &fakeCooldownRepo{}
	tx2 := &fakeTransactor{}
	uc2 := New(Deps{
		Grabs: g2, Cooldowns: c2, Tx: tx2,
		GUIDCooldownLookup: lookup, Logger: quietLogger(),
	})

	err = uc2.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID: rec2.DownloadID, OccurredAt: occurred,
	})
	require.NoError(t, err)
	require.Len(t, c2.sets, 1)
	assert.Equal(t, occurred.Add(96*time.Hour), c2.sets[0].ExpiresAt,
		"post-swap call must observe the new duration — lookup must NOT cache")
}

func TestProcess_Grabbed_ValidHash_FromNull_PopulatesRow(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	g := &fakeGrabRepo{match: rec}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	const hash = "0123456789abcdef0123456789abcdef01234567"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type:         domainwebhook.EventTypeGrabbed,
		InstanceName: "main",
		DownloadID:   hash,
		SeriesID:     122,
		SeasonNumber: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.matchCalls)
	assert.Equal(t, 1, g.hashUpdateCall)
	assert.Equal(t, rec.ID, g.hashUpdateID)
	assert.Equal(t, hash, g.hashUpdateVal,
		"the lowercased 40-char hex hash must be passed to UpdateTorrentHash")
	// Status side-effects: none.
	assert.Equal(t, 0, g.updateCalls, "OnGrab webhook never mutates status")
}

func TestProcess_Grabbed_ValidHash_LowercasesUpper(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	g := &fakeGrabRepo{match: rec}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	const upper = "0123456789ABCDEF0123456789ABCDEF01234567"
	const lower = "0123456789abcdef0123456789abcdef01234567"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: upper,
	})
	require.NoError(t, err)
	require.Equal(t, 1, g.hashUpdateCall)
	assert.Equal(t, lower, g.hashUpdateVal,
		"uppercase hex must be normalised before persist")
}

func TestProcess_Grabbed_HashAlreadySet_NoUpdate(t *testing.T) {
	t.Parallel()
	existing := "0123456789abcdef0123456789abcdef01234567"
	rec := sampleRecord()
	rec.TorrentHash = &existing
	g := &fakeGrabRepo{match: rec}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	const newer = "fedcba9876543210fedcba9876543210fedcba98"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: newer,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.matchCalls)
	assert.Equal(t, 0, g.hashUpdateCall,
		"row whose torrent_hash is already populated must skip the update (D63 first-seen-wins)")
}

func TestProcess_Grabbed_OrphanNoRow_NoUpdate_NoError(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{matchErr: ports.ErrNotFound}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: "0123456789abcdef0123456789abcdef01234567",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.matchCalls)
	assert.Equal(t, 0, g.hashUpdateCall)
}

func TestProcess_Grabbed_MatchErrorIsTransient(t *testing.T) {
	t.Parallel()
	g := &fakeGrabRepo{matchErr: errors.New("db down")}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: "0123456789abcdef0123456789abcdef01234567",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrDBUnavailable)
	assert.Equal(t, 0, g.hashUpdateCall)
}

func TestProcess_Grabbed_UpdateErrorIsTransient(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	g := &fakeGrabRepo{match: rec, hashUpdateErr: errors.New("update boom")}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: "0123456789abcdef0123456789abcdef01234567",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrDBUnavailable)
	assert.Equal(t, 1, g.hashUpdateCall)
}

func TestProcess_Grabbed_RowVanishedBetweenLookupAndUpdate_NoError(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	g := &fakeGrabRepo{match: rec, hashUpdateErr: ports.ErrNotFound}
	uc := newUseCase(t, g, &fakeCooldownRepo{}, &fakeTransactor{})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID: "0123456789abcdef0123456789abcdef01234567",
	})
	require.NoError(t, err, "ErrNotFound from UpdateTorrentHash is a benign race, not a failure")
	assert.Equal(t, 1, g.hashUpdateCall)
}

func TestProcess_SeriesAdd_UpsertsCache(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesAdd, InstanceName: "main",
		SeriesID: 42, SeriesTitle: "Black-ish", SeriesTitleSlug: "black-ish",
		SeriesTVDBID: 269578, SeriesIMDBID: "tt3487356",
		RawEventType: "SeriesAdd",
	})
	require.NoError(t, err)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Equal(t, 1, cache.upsertCalls)
	assert.Equal(t, domain.InstanceName("main"), cache.upsertedEntry.InstanceName)
	assert.Equal(t, domain.SonarrSeriesID(42), cache.upsertedEntry.SonarrSeriesID)
	assert.Equal(t, "Black-ish", cache.upsertedEntry.Title)
	assert.Equal(t, "black-ish", cache.upsertedEntry.TitleSlug)
	require.NotNil(t, cache.upsertedEntry.TVDBID)
	assert.Equal(t, domain.TVDBID(269578), *cache.upsertedEntry.TVDBID)
	require.NotNil(t, cache.upsertedEntry.IMDBID)
	assert.Equal(t, "tt3487356", *cache.upsertedEntry.IMDBID)
	assert.True(t, cache.upsertedEntry.Monitored)
}

func TestProcess_SeriesAdd_UpsertErrorIsSwallowed(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{upsertErr: errors.New("db down")}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesAdd, InstanceName: "main",
		SeriesID: 42, SeriesTitle: "X",
	})
	require.NoError(t, err, "cache failure must NOT surface — Sonarr would retry-storm")
	cache.mu.Lock()
	defer cache.mu.Unlock()
	assert.Equal(t, 1, cache.upsertCalls)
}

func TestProcess_SeriesAdd_NilCache_NoOp(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{})
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesAdd, InstanceName: "main", SeriesID: 42,
	})
	require.NoError(t, err)
}

func TestProcess_SeriesAdd_MissingID_NoOp(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesAdd, InstanceName: "main", SeriesID: 0,
	})
	require.NoError(t, err)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	assert.Equal(t, 0, cache.upsertCalls, "no PK → no upsert attempt")
}

func TestProcess_SeriesDelete_SoftDeletesCache(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesDeleted, InstanceName: "main", SeriesID: 42,
	})
	require.NoError(t, err)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Equal(t, 1, cache.deleteCalls)
	assert.Equal(t, domain.InstanceName("main"), cache.deletedInst)
	assert.Equal(t, domain.SonarrSeriesID(42), cache.deletedID)
}

func TestProcess_SeriesDelete_ErrorIsSwallowed(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{deleteErr: errors.New("disk full")}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesDeleted, InstanceName: "main", SeriesID: 42,
	})
	require.NoError(t, err)
}

func TestProcess_SeriesDelete_NilCache_NoOp(t *testing.T) {
	t.Parallel()
	uc := newUseCase(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{})
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesDeleted, InstanceName: "main", SeriesID: 42,
	})
	require.NoError(t, err)
}

func TestProcess_SeriesDelete_MissingID_NoOp(t *testing.T) {
	t.Parallel()
	cache := &fakeSeriesCache{}
	uc := newUseCaseWithCache(t, &fakeGrabRepo{}, &fakeCooldownRepo{}, &fakeTransactor{}, cache)
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeSeriesDeleted, InstanceName: "main", SeriesID: 0,
	})
	require.NoError(t, err)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	assert.Equal(t, 0, cache.deleteCalls)
}

func TestProcess_Grabbed_SizeBytes_FromNull_Populates(t *testing.T) {
	t.Parallel()
	repo := &fakeGrabRepo{
		match: grab.Record{
			ID: uuid.New(), InstanceName: "main", Status: grab.StatusGrabbed,
		},
	}
	uc := New(Deps{Grabs: repo})
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID:  "0123456789abcdef0123456789abcdef01234567",
		ReleaseSize: 13_325_829_734,
	})
	require.NoError(t, err)
	require.Equal(t, int64(13_325_829_734), repo.lastUpdatedSize)
}

func TestProcess_Grabbed_SizeBytes_AlreadySet_NoUpdate(t *testing.T) {
	t.Parallel()
	existing := int64(9_999_999)
	repo := &fakeGrabRepo{
		match: grab.Record{
			ID: uuid.New(), InstanceName: "main", Status: grab.StatusGrabbed,
			SizeBytes: &existing,
		},
	}
	uc := New(Deps{Grabs: repo})
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID:  "0123456789abcdef0123456789abcdef01234567",
		ReleaseSize: 13_325_829_734,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), repo.lastUpdatedSize, "must not overwrite existing")
}

func TestProcess_Grabbed_SizeBytes_ZeroPayload_NoUpdate(t *testing.T) {
	t.Parallel()
	repo := &fakeGrabRepo{
		match: grab.Record{
			ID: uuid.New(), InstanceName: "main", Status: grab.StatusGrabbed,
		},
	}
	uc := New(Deps{Grabs: repo})
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeGrabbed, InstanceName: "main",
		DownloadID:  "0123456789abcdef0123456789abcdef01234567",
		ReleaseSize: 0,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), repo.lastUpdatedSize)
}

// fakeTorrentSeriesMap records every UpsertTx call.
type fakeTorrentSeriesMap struct {
	mu   sync.Mutex
	rows []torrentsync.MapRow
	err  error
}

func (f *fakeTorrentSeriesMap) Upsert(_ context.Context, row torrentsync.MapRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)
	return nil
}
func (f *fakeTorrentSeriesMap) UpsertTx(ctx context.Context, row torrentsync.MapRow) error {
	return f.Upsert(ctx, row)
}

func TestProcess_Grabbed_WritesTorrentSeriesMapInSameTx(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	rec.SeriesID = 122
	rec.SeasonNumber = 2
	g := &fakeGrabRepo{match: rec}
	tx := &fakeTransactor{}
	tsm := &fakeTorrentSeriesMap{}
	uc := New(Deps{
		Grabs:              g,
		Cooldowns:          &fakeCooldownRepo{},
		Tx:                 tx,
		TorrentSeriesMap:   tsm,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})

	const hash = "0123456789abcdef0123456789abcdef01234567"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type:         domainwebhook.EventTypeGrabbed,
		InstanceName: "main",
		DownloadID:   hash,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.hashUpdateCall)
	assert.Equal(t, 1, tx.called, "hash + map write must run in a single tx")
	assert.True(t, tx.committed)
	require.Len(t, tsm.rows, 1)
	got := tsm.rows[0]
	assert.Equal(t, domain.InstanceName("main"), got.Instance)
	assert.Equal(t, hash, got.Hash)
	assert.Equal(t, domain.SonarrSeriesID(122), got.SeriesID)
	assert.Equal(t, 2, got.SeasonNumber)
	assert.Equal(t, torrentsync.MapSourceWebhook, got.Source)
}

func TestProcess_Grabbed_MapWriteFailureRollsBackHashUpdate(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	rec.SeriesID = 122
	g := &fakeGrabRepo{match: rec}
	tx := &fakeTransactor{}
	tsm := &fakeTorrentSeriesMap{err: errors.New("db down")}
	uc := New(Deps{
		Grabs:              g,
		Cooldowns:          &fakeCooldownRepo{},
		Tx:                 tx,
		TorrentSeriesMap:   tsm,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})

	const hash = "0123456789abcdef0123456789abcdef01234567"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type:         domainwebhook.EventTypeGrabbed,
		InstanceName: "main",
		DownloadID:   hash,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrDBUnavailable)
	assert.Equal(t, 1, tx.called)
	assert.False(t, tx.committed, "tx must NOT commit when map write fails")
}

func TestProcess_Grabbed_NoMapWriteWhenSeriesIDZero(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	rec.TorrentHash = nil
	rec.SeriesID = 0 // unknown
	g := &fakeGrabRepo{match: rec}
	tx := &fakeTransactor{}
	tsm := &fakeTorrentSeriesMap{}
	uc := New(Deps{
		Grabs:              g,
		Cooldowns:          &fakeCooldownRepo{},
		Tx:                 tx,
		TorrentSeriesMap:   tsm,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})

	const hash = "0123456789abcdef0123456789abcdef01234567"
	err := uc.Process(context.Background(), domainwebhook.Event{
		Type:         domainwebhook.EventTypeGrabbed,
		InstanceName: "main",
		DownloadID:   hash,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.hashUpdateCall)
	assert.Empty(t, tsm.rows, "missing series_id → no map row")
}

func TestProcess_Grabbed_HashAlreadySet_NoTxNoMapWrite(t *testing.T) {
	t.Parallel()
	existing := "0123456789abcdef0123456789abcdef01234567"
	rec := sampleRecord()
	rec.TorrentHash = &existing
	rec.SeriesID = 1
	g := &fakeGrabRepo{match: rec}
	tx := &fakeTransactor{}
	tsm := &fakeTorrentSeriesMap{}
	uc := New(Deps{
		Grabs:              g,
		Cooldowns:          &fakeCooldownRepo{},
		Tx:                 tx,
		TorrentSeriesMap:   tsm,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type:         domainwebhook.EventTypeGrabbed,
		InstanceName: "main",
		DownloadID:   "fedcba9876543210fedcba9876543210fedcba98",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, g.hashUpdateCall)
	assert.Equal(t, 0, tx.called, "no tx when hash already set")
	assert.Empty(t, tsm.rows)
}
