package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// baseTime is a fixed reference instant so FIFO / due assertions are
// deterministic (SQLite CURRENT_TIMESTAMP is second-resolution; we set
// created_at / next_attempt_at explicitly rather than relying on it).
func baseTime() time.Time {
	return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
}

// insertRow is a test helper that inserts a pending row with an explicit
// created_at (for FIFO order control) and returns its id by reading the
// row straight back from the model table.
func insertRow(t *testing.T, db *gorm.DB, r *WebhookInboxRepository, instance, event string, createdAt time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	payload := []byte(`{"eventType":"` + event + `","series":{"id":1}}`)
	require.NoError(t, r.Insert(ctx, ports.WebhookInboxRow{
		InstanceName: instance,
		EventType:    event,
		Payload:      payload,
		CreatedAt:    createdAt,
	}))
	var m database.WebhookInboxModel
	require.NoError(t, db.Where("instance_name = ? AND created_at = ?", instance, createdAt).
		First(&m).Error)
	return m.ID
}

func TestWebhookInbox_Insert_Persists(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()

			payload := []byte(`{"eventType":"Grab","series":{"id":42}}`)
			require.NoError(t, repo.Insert(ctx, ports.WebhookInboxRow{
				InstanceName: "main",
				EventType:    "Grab",
				Payload:      payload,
			}))

			var m database.WebhookInboxModel
			require.NoError(t, db.Where("instance_name = ?", "main").First(&m).Error)
			assert.Equal(t, "Grab", m.EventType)
			assert.Equal(t, ports.WebhookInboxStatusPending, m.Status)
			assert.Equal(t, 0, m.Attempts)
			assert.Nil(t, m.NextAttemptAt)
			assert.Nil(t, m.LeaseUntil)
			assert.False(t, m.CreatedAt.IsZero(), "created_at stamped when zero")
			assert.JSONEq(t, string(payload), string(m.Payload))
		})
	}
}

func TestWebhookInbox_Insert_ValidationErrors(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewWebhookInboxRepository(backend.NewDB(t))
			ctx := context.Background()

			// empty instance
			require.Error(t, repo.Insert(ctx, ports.WebhookInboxRow{
				EventType: "Grab", Payload: []byte(`{}`),
			}))
			// empty event_type
			require.Error(t, repo.Insert(ctx, ports.WebhookInboxRow{
				InstanceName: "main", Payload: []byte(`{}`),
			}))
			// empty payload
			require.Error(t, repo.Insert(ctx, ports.WebhookInboxRow{
				InstanceName: "main", EventType: "Grab",
			}))

			var count int64
			require.NoError(t, backend.NewDB(t).Model(&database.WebhookInboxModel{}).Count(&count).Error)
			// note: fresh DB per NewDB call — this asserts nothing leaked
			// into THIS handle; the point is the three Inserts above erred
			// before touching the DB.
			assert.Equal(t, int64(0), count)
		})
	}
}

func TestWebhookInbox_Insert_InTx_Rollback_LeavesNoRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			tx := NewGormTransactor(db)
			ctx := context.Background()

			sentinel := errors.New("force rollback")
			err := tx.Transaction(ctx, func(txctx context.Context) error {
				if e := repo.Insert(txctx, ports.WebhookInboxRow{
					InstanceName: "main", EventType: "Grab", Payload: []byte(`{"a":1}`),
				}); e != nil {
					return e
				}
				return sentinel
			})
			require.ErrorIs(t, err, sentinel)

			var count int64
			require.NoError(t, db.Model(&database.WebhookInboxModel{}).Count(&count).Error)
			assert.Equal(t, int64(0), count, "rolled-back insert left no row")
		})
	}
}

func TestWebhookInbox_ClaimDue_FIFO_And_Limit(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			id1 := insertRow(t, db, repo, "main", "Grab", t0)
			id2 := insertRow(t, db, repo, "main", "Download", t0.Add(1*time.Second))
			id3 := insertRow(t, db, repo, "main", "SeriesAdd", t0.Add(2*time.Second))

			// limit 2 -> the two OLDEST, in created_at order.
			claimed, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 2)
			require.NoError(t, err)
			require.Len(t, claimed, 2)
			assert.Equal(t, id1, claimed[0].ID)
			assert.Equal(t, id2, claimed[1].ID)
			for _, c := range claimed {
				assert.Equal(t, ports.WebhookInboxStatusProcessing, c.Status)
				require.NotNil(t, c.LeaseUntil)
			}

			// the third is still pending -> next claim returns just it.
			claimed2, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, claimed2, 1)
			assert.Equal(t, id3, claimed2[0].ID)
		})
	}
}

func TestWebhookInbox_ClaimDue_RespectsNextAttemptAt(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			// due row: next_attempt_at NULL.
			dueID := insertRow(t, db, repo, "main", "Grab", t0)
			// not-yet-due row: next_attempt_at in the future.
			future := t0.Add(time.Hour)
			notDueID := insertRow(t, db, repo, "main", "Grab", t0.Add(time.Second))
			require.NoError(t, db.Model(&database.WebhookInboxModel{}).
				Where("id = ?", notDueID).
				Update("next_attempt_at", future).Error)

			claimed, err := repo.ClaimDue(ctx, t0.Add(time.Minute), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, claimed, 1)
			assert.Equal(t, dueID, claimed[0].ID, "future next_attempt_at row skipped")
		})
	}
}

func TestWebhookInbox_ClaimDue_NoDoubleClaim(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			insertRow(t, db, repo, "main", "Grab", t0)

			first, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, first, 1)

			// already processing -> not re-claimable.
			second, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)
			assert.Empty(t, second, "processing row not double-claimed")
		})
	}
}

func TestWebhookInbox_MarkSuccess_Deletes(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			id := insertRow(t, db, repo, "main", "Grab", t0)
			require.NoError(t, repo.MarkSuccess(ctx, id))

			var count int64
			require.NoError(t, db.Model(&database.WebhookInboxModel{}).
				Where("id = ?", id).Count(&count).Error)
			assert.Equal(t, int64(0), count)

			// idempotent: deleting a missing row is not an error.
			require.NoError(t, repo.MarkSuccess(ctx, id))
		})
	}
}

func TestWebhookInbox_MarkFailure_BumpsAttempts_Reschedules_Pending(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			id := insertRow(t, db, repo, "main", "Grab", t0)
			_, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)

			retryAt := t0.Add(3 * time.Hour)
			require.NoError(t, repo.MarkFailure(ctx, id, "sonarr 503", retryAt))

			var m database.WebhookInboxModel
			require.NoError(t, db.Where("id = ?", id).First(&m).Error)
			assert.Equal(t, ports.WebhookInboxStatusPending, m.Status)
			assert.Equal(t, 1, m.Attempts)
			assert.Equal(t, "sonarr 503", m.LastError)
			assert.Nil(t, m.LeaseUntil, "lease cleared")
			require.NotNil(t, m.NextAttemptAt)
			assert.WithinDuration(t, retryAt, *m.NextAttemptAt, time.Second)

			// not yet due -> not claimed.
			notYet, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(4*time.Hour), 10)
			require.NoError(t, err)
			assert.Empty(t, notYet)

			// due after retryAt -> claimed again.
			due, err := repo.ClaimDue(ctx, t0.Add(4*time.Hour), t0.Add(5*time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, due, 1)
			assert.Equal(t, id, due[0].ID)
		})
	}
}

func TestWebhookInbox_MarkFailure_UnknownID_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewWebhookInboxRepository(backend.NewDB(t))
			err := repo.MarkFailure(context.Background(), 999999, "x", baseTime())
			assert.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestWebhookInbox_MarkDead_SetsDead_NotReclaimed(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			id := insertRow(t, db, repo, "main", "Grab", t0)
			// claim with an already-expired lease so ReclaimStale would
			// otherwise pick it up.
			_, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(-time.Minute), 10)
			require.NoError(t, err)
			require.NoError(t, repo.MarkDead(ctx, id, "unmappable payload"))

			var m database.WebhookInboxModel
			require.NoError(t, db.Where("id = ?", id).First(&m).Error)
			assert.Equal(t, ports.WebhookInboxStatusDead, m.Status)
			assert.Equal(t, "unmappable payload", m.LastError)
			assert.Nil(t, m.LeaseUntil)

			// dead row is not claimable and not reclaimed.
			claimed, err := repo.ClaimDue(ctx, t0.Add(2*time.Hour), t0.Add(3*time.Hour), 10)
			require.NoError(t, err)
			assert.Empty(t, claimed)

			n, err := repo.ReclaimStale(ctx, t0.Add(2*time.Hour))
			require.NoError(t, err)
			assert.Equal(t, int64(0), n, "dead row not reclaimed")
		})
	}
}

func TestWebhookInbox_MarkDead_UnknownID_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewWebhookInboxRepository(backend.NewDB(t))
			err := repo.MarkDead(context.Background(), 999999, "x")
			assert.ErrorIs(t, err, ports.ErrNotFound)
		})
	}
}

func TestWebhookInbox_ReclaimStale_RecoversExpired_LeavesFresh(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewWebhookInboxRepository(db)
			ctx := context.Background()
			t0 := baseTime()

			// staleID: claimed with an already-expired lease.
			staleID := insertRow(t, db, repo, "main", "Grab", t0)
			_, err := repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(-time.Minute), 10)
			require.NoError(t, err)

			// freshID: claimed with a future lease (claimed in a 2nd batch,
			// so only THIS row gets the fresh lease).
			freshID := insertRow(t, db, repo, "main", "Grab", t0.Add(time.Second))
			_, err = repo.ClaimDue(ctx, t0.Add(time.Hour), t0.Add(2*time.Hour), 10)
			require.NoError(t, err)

			n, err := repo.ReclaimStale(ctx, t0.Add(time.Hour))
			require.NoError(t, err)
			assert.Equal(t, int64(1), n, "only the expired-lease row reclaimed")

			var stale, fresh database.WebhookInboxModel
			require.NoError(t, db.Where("id = ?", staleID).First(&stale).Error)
			require.NoError(t, db.Where("id = ?", freshID).First(&fresh).Error)
			assert.Equal(t, ports.WebhookInboxStatusPending, stale.Status)
			assert.Nil(t, stale.LeaseUntil)
			assert.Equal(t, ports.WebhookInboxStatusProcessing, fresh.Status, "fresh lease untouched")

			// reclaimed row is claimable again.
			claimed, err := repo.ClaimDue(ctx, t0.Add(2*time.Hour), t0.Add(3*time.Hour), 10)
			require.NoError(t, err)
			require.Len(t, claimed, 1)
			assert.Equal(t, staleID, claimed[0].ID)
		})
	}
}
