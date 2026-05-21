package webhook

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
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/grab"
	domainwebhook "github.com/alexmorbo/seasonfill/domain/webhook"
)

type fakeGrabRepo struct {
	mu            sync.Mutex
	match         grab.Record
	matchErr      error
	matchKey      ports.MatchKey
	matchCalls    int
	updateErr     error
	updateID      uuid.UUID
	updateStatus  grab.Status
	updateMessage string
	updateCalls   int
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

func newUseCase(t *testing.T, g *fakeGrabRepo, c *fakeCooldownRepo, tx *fakeTransactor) *UseCase {
	t.Helper()
	return New(Deps{
		Grabs:                 g,
		Cooldowns:             c,
		Tx:                    tx,
		GUIDAfterFailedImport: 48 * time.Hour,
		Logger:                quietLogger(),
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

func TestProcess_UnsupportedAndGrabbed_NoCalls(t *testing.T) {
	t.Parallel()
	for _, et := range []domainwebhook.EventType{
		domainwebhook.EventTypeUnsupported,
		domainwebhook.EventTypeGrabbed,
	} {
		g := &fakeGrabRepo{}
		c := &fakeCooldownRepo{}
		tx := &fakeTransactor{}
		uc := newUseCase(t, g, c, tx)

		err := uc.Process(context.Background(), domainwebhook.Event{
			Type: et, InstanceName: "main", RawEventType: "Rename",
		})
		require.NoError(t, err, "event type %q", et)
		assert.Equal(t, 0, g.matchCalls, "event type %q", et)
		assert.Empty(t, c.sets, "event type %q", et)
		assert.Equal(t, 0, tx.called, "event type %q", et)
	}
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
		GUIDAfterFailedImport: 48 * time.Hour, Logger: quietLogger(),
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
		GUIDAfterFailedImport: 0, Logger: quietLogger(),
	})

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main", DownloadID: rec.DownloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, g.updateCalls)
	assert.Empty(t, c.sets)
	assert.True(t, tx.committed)
}
