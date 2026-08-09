package grab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingrab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ADR-0016 N2.1/N2.2 — transactional-outbox emit tests for the grab usecase.
//
// The staging transactor below models a real DB transaction with in-memory
// fakes: writes made inside the work closure are buffered and only applied
// ("committed") when the closure succeeds. A closure error drops the buffer
// ("rollback"). This lets a unit test assert the transactional-outbox
// invariant — the outbox row commits atomically with the source write, and a
// rollback of the source drops the outbox row too — without a live database.

type txBufferKey struct{}

type txBuffer struct{ pending []func() }

func (b *txBuffer) do(f func()) { b.pending = append(b.pending, f) }

type stagingTransactor struct {
	calls int
}

func (tr *stagingTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	tr.calls++
	buf := &txBuffer{}
	if err := fn(context.WithValue(ctx, txBufferKey{}, buf)); err != nil {
		return err // rollback: pending writes discarded
	}
	for _, f := range buf.pending {
		f()
	}
	return nil
}

// txGrabRepo embeds the file-level fakeGrabRepo for the full GrabRepository
// surface and overrides Create to be tx-aware (buffers on commit).
type txGrabRepo struct {
	*fakeGrabRepo
	mu        sync.Mutex
	committed []domaingrab.Record
	createErr error
}

func (r *txGrabRepo) Create(ctx context.Context, rec domaingrab.Record) error {
	if r.createErr != nil {
		return r.createErr
	}
	apply := func() {
		r.mu.Lock()
		r.committed = append(r.committed, rec)
		r.mu.Unlock()
	}
	if buf, ok := ctx.Value(txBufferKey{}).(*txBuffer); ok {
		buf.do(apply)
		return nil
	}
	apply()
	return nil
}

func (r *txGrabRepo) committedRecords() []domaingrab.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domaingrab.Record(nil), r.committed...)
}

type stagingOutbox struct {
	mu        sync.Mutex
	committed []ports.OutboxRow
	insertErr error
}

func (o *stagingOutbox) Insert(ctx context.Context, row ports.OutboxRow) error {
	if o.insertErr != nil {
		return o.insertErr
	}
	apply := func() {
		o.mu.Lock()
		o.committed = append(o.committed, row)
		o.mu.Unlock()
	}
	if buf, ok := ctx.Value(txBufferKey{}).(*txBuffer); ok {
		buf.do(apply)
		return nil
	}
	apply()
	return nil
}

func (o *stagingOutbox) rows() []ports.OutboxRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ports.OutboxRow(nil), o.committed...)
}

// erroringOriginRepo forces the success-path origin upsert to fail so the
// grab.ok atomicity test can trigger a rollback.
type erroringOriginRepo struct{ err error }

func (r erroringOriginRepo) Get(context.Context, shareddomain.InstanceName, shareddomain.SonarrSeriesID, int) (ports.OriginRelease, bool, error) {
	return ports.OriginRelease{}, false, nil
}
func (r erroringOriginRepo) Upsert(context.Context, ports.OriginRelease) error { return r.err }

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func newOutboxUC(grabs ports.GrabRepository, origins ports.OriginReleaseRepository, tx ports.Transactor, outbox ports.OutboxEmitter) *UseCase {
	uc := NewUseCase(grabs, &fakeCooldownRepo{}, origins,
		fakeClassifier{
			transient: func(e error) bool { return errors.Is(e, errTransient) },
			is4xx:     func(e error) bool { return errors.Is(e, err4xx) },
		},
		quietLogger(),
	).WithSleeper(noopSleep)
	if tx != nil {
		uc.WithTransactor(tx)
	}
	if outbox != nil {
		uc.WithOutbox(outbox)
	}
	return uc
}

func TestExecute_GrabFailed_EmitsOutboxInTx(t *testing.T) {
	t.Parallel()
	grabs := &txGrabRepo{fakeGrabRepo: &fakeGrabRepo{}}
	tx := &stagingTransactor{}
	outbox := &stagingOutbox{}
	uc := newOutboxUC(grabs, &fakeOriginRepo{}, tx, outbox)

	sonarr := &fakeSonarrGrab{errors: []error{err4xx}} // permanent failure
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err)
	assert.Equal(t, domaingrab.StatusGrabFailed, out.Record.Status)
	require.Equal(t, 1, tx.calls, "failure path must open exactly one tx")

	recs := grabs.committedRecords()
	require.Len(t, recs, 1, "grab_record committed")

	rows := outbox.rows()
	require.Len(t, rows, 1, "one outbox row committed atomically with the grab_record")
	assert.Equal(t, "grab.failed", rows[0].EventType)

	var p map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Payload, &p))
	assert.Equal(t, "Hijack", p["series_title"])
	assert.EqualValues(t, 2, p["season"])
	assert.Contains(t, p["error"], "4xx")
}

func TestExecute_GrabFailed_NilOutbox_StillCreates(t *testing.T) {
	t.Parallel()
	grabs := &txGrabRepo{fakeGrabRepo: &fakeGrabRepo{}}
	tx := &stagingTransactor{}
	uc := newOutboxUC(grabs, &fakeOriginRepo{}, tx, nil) // outbox nil

	sonarr := &fakeSonarrGrab{errors: []error{err4xx}}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err)
	require.Len(t, grabs.committedRecords(), 1, "grab_record still written when outbox is nil")
}

func TestExecute_GrabOK_EmitsOutboxInPersistSuccessTx(t *testing.T) {
	t.Parallel()
	grabs := &txGrabRepo{fakeGrabRepo: &fakeGrabRepo{}}
	tx := &stagingTransactor{}
	outbox := &stagingOutbox{}
	uc := newOutboxUC(grabs, &fakeOriginRepo{}, tx, outbox)

	sonarr := &fakeSonarrGrab{} // first-attempt success
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.NoError(t, out.Err)
	assert.Equal(t, domaingrab.StatusGrabbed, out.Record.Status)
	require.Len(t, grabs.committedRecords(), 1)

	rows := outbox.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "grab.ok", rows[0].EventType)

	var p map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Payload, &p))
	assert.Equal(t, "Hijack", p["series_title"])
	assert.EqualValues(t, 2, p["season"])
	assert.Equal(t, "RT", p["indexer"])
}

func TestExecute_GrabOK_RollbackDropsBothRecordAndOutbox(t *testing.T) {
	t.Parallel()
	grabs := &txGrabRepo{fakeGrabRepo: &fakeGrabRepo{}}
	tx := &stagingTransactor{}
	outbox := &stagingOutbox{}
	// origin upsert fails -> persistSuccess work closure returns error ->
	// the staging transactor discards the buffered grab_record AND (had it
	// been reached) any outbox write. Insert sits after the origin upsert,
	// so it is never reached; either way both stores must stay empty.
	origins := erroringOriginRepo{err: errors.New("origin boom")}
	uc := newOutboxUC(grabs, origins, tx, outbox)

	sonarr := &fakeSonarrGrab{}
	out := uc.Execute(context.Background(), newInput(sonarr))

	require.Error(t, out.Err, "persist failure surfaces as Output.Err")
	assert.Empty(t, grabs.committedRecords(), "grab_record rolled back")
	assert.Empty(t, outbox.rows(), "outbox row rolled back (atomicity)")
}
