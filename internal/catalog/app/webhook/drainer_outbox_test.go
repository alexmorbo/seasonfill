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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/clock"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// ADR-0016 N2.5 — inbox.dead_letter transactional-outbox emit tests.

// dedupOutbox models the persistence-layer storm-collapse: a second pending
// row with an already-seen dedup_key is a silent no-op.
type dedupOutbox struct {
	mu   sync.Mutex
	rows []ports.OutboxRow
}

func (o *dedupOutbox) Insert(_ context.Context, row ports.OutboxRow) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if row.DedupKey != nil {
		for _, r := range o.rows {
			if r.DedupKey != nil && *r.DedupKey == *row.DedupKey {
				return nil // storm-collapse
			}
		}
	}
	o.rows = append(o.rows, row)
	return nil
}

func (o *dedupOutbox) snapshot() []ports.OutboxRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ports.OutboxRow(nil), o.rows...)
}

func deadLetterDrainer(mock *ports.WebhookInboxRepositoryMock, outbox ports.OutboxEmitter, tx ports.Transactor, fake *clock.Fake) *Drainer {
	if mock.ReclaimStaleFunc == nil {
		mock.ReclaimStaleFunc = func(context.Context, time.Time) (int64, error) { return 0, nil }
	}
	proc := func(context.Context, webhook.Event) error { return errors.New("logic boom") } // non-retryable -> dead-letter
	return NewDrainer(DrainerDeps{
		Inbox:         mock,
		Process:       proc,
		MapEvent:      mapValid,
		Clock:         fake,
		Outbox:        outbox,
		Tx:            tx,
		AttemptCap:    3,
		PerJobTimeout: 5 * time.Second,
		Tick:          2 * time.Second,
		ClaimLimit:    50,
	})
}

func TestDrainer_DeadLetter_EmitsInboxDeadLetter(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		ClaimDueFunc: oneShotClaim([]ports.WebhookInboxRow{{ID: 42, InstanceName: "main", EventType: "Download", Payload: []byte("{}"), Attempts: 0}}),
		MarkDeadFunc: func(context.Context, int64, string) error { return nil },
	}
	outbox := &fakeOutbox{}
	d := deadLetterDrainer(mock, outbox, nil, fake)

	d.drainOnce(context.Background())

	require.Len(t, mock.MarkDeadCalls(), 1)
	rows := outbox.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, "inbox.dead_letter", rows[0].EventType)
	require.NotNil(t, rows[0].DedupKey)
	assert.Equal(t, "inbox_dead:42", *rows[0].DedupKey)

	var p map[string]any
	require.NoError(t, json.Unmarshal(rows[0].Payload, &p))
	assert.EqualValues(t, 42, p["inbox_id"])
	assert.Equal(t, "inbox.dead_letter", p["event_type"])
}

func TestDrainer_DeadLetter_DedupCollapsesCascade(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		MarkDeadFunc: func(context.Context, int64, string) error { return nil },
	}
	outbox := &dedupOutbox{}
	d := deadLetterDrainer(mock, outbox, nil, fake)

	// Same inbox id dead-lettered twice (drain re-attempts before the row
	// dies) → one dedup_key → the store collapses to a single ping.
	d.markDead(context.Background(), 7, errors.New("boom"), d.logger)
	d.markDead(context.Background(), 7, errors.New("boom"), d.logger)

	rows := outbox.snapshot()
	require.Len(t, rows, 1, "cascade with the same inbox id collapses to one row")
	assert.Equal(t, "inbox_dead:7", *rows[0].DedupKey)
}

func TestDrainer_DeadLetter_MarkDeadFailure_NoOutboxRow(t *testing.T) {
	t.Parallel()
	fake := clock.NewFake(testStart())
	mock := &ports.WebhookInboxRepositoryMock{
		MarkDeadFunc: func(context.Context, int64, string) error { return errors.New("mark dead boom") },
	}
	outbox := &fakeOutbox{}
	tx := &fakeTransactor{}
	d := deadLetterDrainer(mock, outbox, tx, fake)

	d.markDead(context.Background(), 9, errors.New("boom"), d.logger)

	assert.Empty(t, outbox.snapshot(), "MarkDead failure aborts before the outbox Insert")
	assert.False(t, tx.committed, "tx must not commit when MarkDead fails")
}
