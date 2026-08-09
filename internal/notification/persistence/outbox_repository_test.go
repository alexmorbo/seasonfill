package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func strptr(s string) *string { return &s }

func TestOutboxRepository_InsertFetch_FIFO(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			base := time.Now().UTC().Add(-time.Hour)
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{
				EventType: "grab.failed", Payload: []byte(`{"a":1}`), CreatedAt: base,
			}))
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{
				EventType: "import.failed", Payload: []byte(`{"b":2}`), CreatedAt: base.Add(time.Minute),
			}))

			rows, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			require.Len(t, rows, 2)
			assert.Equal(t, "grab.failed", rows[0].EventType) // FIFO by created_at
			assert.Equal(t, "import.failed", rows[1].EventType)
		})
	}
}

func TestOutboxRepository_FetchDue_NextAttemptWindow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()
			now := time.Now().UTC()

			future := now.Add(time.Hour)
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{
				EventType: "grab.failed", Payload: []byte(`{}`), NextAttemptAt: &future,
			}))
			// NULL next_attempt_at → due now.
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{
				EventType: "grab.ok", Payload: []byte(`{}`),
			}))

			rows, err := repo.FetchDueBatch(ctx, now, 10)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "grab.ok", rows[0].EventType)
		})
	}
}

func TestOutboxRepository_Reschedule(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "grab.failed", Payload: []byte(`{}`)}))
			rows, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			id := rows[0].ID

			next := time.Now().UTC().Add(2 * time.Second)
			require.NoError(t, repo.Reschedule(ctx, id, next))

			// Not due now (pushed forward), still pending.
			due, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			assert.Empty(t, due)

			// Due after next_attempt_at.
			due, err = repo.FetchDueBatch(ctx, next.Add(time.Second), 10)
			require.NoError(t, err)
			require.Len(t, due, 1)
			assert.Equal(t, 1, due[0].Attempts) // attempts++

			n, err := repo.CountPending(ctx)
			require.NoError(t, err)
			assert.EqualValues(t, 1, n)
		})
	}
}

func TestOutboxRepository_MarkSentAndDead(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "grab.failed", Payload: []byte(`{}`)}))
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "import.failed", Payload: []byte(`{}`)}))
			rows, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			require.Len(t, rows, 2)

			require.NoError(t, repo.MarkSent(ctx, rows[0].ID)) // delete-on-success
			require.NoError(t, repo.MarkDead(ctx, rows[1].ID)) // status=dead

			due, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			assert.Empty(t, due) // sent deleted, dead excluded

			// MarkSent is idempotent.
			require.NoError(t, repo.MarkSent(ctx, rows[0].ID))
		})
	}
}

func TestOutboxRepository_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			err := repo.Reschedule(ctx, 999, time.Now().UTC())
			assert.True(t, errors.Is(err, ports.ErrNotFound))
			err = repo.MarkDead(ctx, 999)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestOutboxRepository_Dedup(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			dk := "inbox_dead:7"
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "inbox.dead_letter", Payload: []byte(`{}`), DedupKey: strptr(dk)}))
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "inbox.dead_letter", Payload: []byte(`{}`), DedupKey: strptr(dk)}))

			rows, err := repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			require.Len(t, rows, 1, "second insert with same pending dedup_key collapses")

			// After the first is sent, the window reopens.
			require.NoError(t, repo.MarkSent(ctx, rows[0].ID))
			require.NoError(t, repo.Insert(ctx, ports.OutboxRow{EventType: "inbox.dead_letter", Payload: []byte(`{}`), DedupKey: strptr(dk)}))
			rows, err = repo.FetchDueBatch(ctx, time.Now().UTC(), 10)
			require.NoError(t, err)
			require.Len(t, rows, 1)
		})
	}
}

func TestOutboxRepository_Insert_Validation(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			ctx := context.Background()

			assert.Error(t, repo.Insert(ctx, ports.OutboxRow{Payload: []byte(`{}`)}))    // empty event_type
			assert.Error(t, repo.Insert(ctx, ports.OutboxRow{EventType: "grab.failed"})) // empty payload
		})
	}
}

// TestOutboxRepository_TransactionalOutbox proves Insert enlists in the
// caller-opened GormTransactor.Transaction via dbtx: when the work fn returns
// an error the tx rolls back and NO outbox row persists.
func TestOutboxRepository_TransactionalOutbox(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewOutboxRepository(db)
			txr := catalogpersistence.NewGormTransactor(db)
			ctx := context.Background()

			wantErr := errors.New("force rollback")
			err := txr.Transaction(ctx, func(txCtx context.Context) error {
				if ierr := repo.Insert(txCtx, ports.OutboxRow{EventType: "grab.failed", Payload: []byte(`{}`)}); ierr != nil {
					return ierr
				}
				return wantErr // abort → rollback
			})
			require.ErrorIs(t, err, wantErr)

			n, err := repo.CountPending(ctx)
			require.NoError(t, err)
			assert.EqualValues(t, 0, n, "rolled-back Insert must leave no row")

			// Committing path persists.
			require.NoError(t, txr.Transaction(ctx, func(txCtx context.Context) error {
				return repo.Insert(txCtx, ports.OutboxRow{EventType: "grab.failed", Payload: []byte(`{}`)})
			}))
			n, err = repo.CountPending(ctx)
			require.NoError(t, err)
			assert.EqualValues(t, 1, n)
		})
	}
}
