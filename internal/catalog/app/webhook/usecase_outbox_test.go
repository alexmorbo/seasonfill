package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// ADR-0016 N2.3 — import.failed transactional-outbox emit test.

type fakeOutbox struct {
	mu        sync.Mutex
	rows      []ports.OutboxRow
	insertErr error
}

func (o *fakeOutbox) Insert(_ context.Context, row ports.OutboxRow) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.insertErr != nil {
		return o.insertErr
	}
	o.rows = append(o.rows, row)
	return nil
}

func (o *fakeOutbox) snapshot() []ports.OutboxRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ports.OutboxRow(nil), o.rows...)
}

func newOutboxUseCase(g *fakeGrabRepo, c *fakeCooldownRepo, tx *fakeTransactor, o ports.OutboxEmitter) *UseCase {
	return New(Deps{
		Grabs:              g,
		Cooldowns:          c,
		Tx:                 tx,
		GUIDCooldownLookup: fixedLookup(),
		Logger:             quietLogger(),
		Outbox:             o,
	})
}

func TestProcess_ImportFailed_EmitsOutboxInTx(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	outbox := &fakeOutbox{}
	uc := newOutboxUseCase(g, c, tx, outbox)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID:   rec.DownloadID,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		Message:      "missing audio",
		OccurredAt:   time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, 1, g.updateCalls)
	assert.Equal(t, grab.StatusImportFailed, g.updateStatus)
	require.Equal(t, 1, tx.called, "emit must run inside the Process tx")

	rows := outbox.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "import.failed", rows[0].EventType)

	var p map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Payload, &p))
	assert.Equal(t, "Hijack", p["series_title"])
	assert.EqualValues(t, 2, p["season"])
	assert.Equal(t, "missing audio", p["message"])
}

func TestProcess_ImportFailed_NilOutbox_NoPanic(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	uc := newOutboxUseCase(g, c, tx, nil) // outbox nil

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID: rec.DownloadID, Message: "missing audio",
		OccurredAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, 1, g.updateCalls, "status update still happens with nil outbox")
	require.Len(t, c.sets, 1, "cooldown still written with nil outbox")
}

// A failing emit aborts the tx — the whole Process returns an error, so a
// real DB rolls back the status update + cooldown together with the outbox
// row (transactional coupling).
func TestProcess_ImportFailed_EmitError_AbortsTx(t *testing.T) {
	t.Parallel()
	rec := sampleRecord()
	g := &fakeGrabRepo{match: rec}
	c := &fakeCooldownRepo{}
	tx := &fakeTransactor{}
	outbox := &fakeOutbox{insertErr: errors.New("outbox boom")}
	uc := newOutboxUseCase(g, c, tx, outbox)

	err := uc.Process(context.Background(), domainwebhook.Event{
		Type: domainwebhook.EventTypeImportFailed, InstanceName: "main",
		DownloadID: rec.DownloadID, SeriesTitle: "Hijack", SeasonNumber: 2,
		Message:    "missing audio",
		OccurredAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
	})
	require.Error(t, err, "emit failure must abort the Process tx")
	assert.False(t, tx.committed, "tx must not commit when the emit fails")
}
