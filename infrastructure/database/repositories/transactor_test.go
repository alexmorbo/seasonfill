package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	grabpersistence "github.com/alexmorbo/seasonfill/internal/grab/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	watchdogpersistence "github.com/alexmorbo/seasonfill/internal/watchdog/persistence"
)

var errForcedMidTx = errors.New("forced mid-transaction failure")

// TestGormTransactor_Rollback_OnMiddleWriteFailure is the M-7 regression canary.
// It proves that when cooldowns.Set fails inside the transaction the earlier
// grabs.Create is rolled back — i.e. all three writes are atomic.
//
// With the pre-fix code (repositories using r.db.WithContext instead of
// dbFromContext) grabs.Create would auto-commit on Postgres before the tx
// rolls back. On SQLite this test would also fail because the grab row would
// survive even though Transaction returned an error.
func TestGormTransactor_Rollback_OnMiddleWriteFailure(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)

			grabRepo := grabpersistence.NewGrabRepository(db)
			cooldownRepo := &failingCooldownRepo{inner: watchdogpersistence.NewCooldownRepository(db)}
			tx := NewGormTransactor(db)

			rec := grab.Record{
				ID:           uuid.New(),
				InstanceName: "main",
				SeriesID:     122,
				SeriesTitle:  "Hijack",
				SeasonNumber: 2,
				ReleaseGUID:  "g-rollback",
				ReleaseTitle: "Pack",
				IndexerID:    3,
				IndexerName:  "RT",
				Status:       grab.StatusGrabbed,
				ScanRunID:    uuid.New(),
				Attempts:     1,
				CreatedAt:    time.Now().UTC(),
				UpdatedAt:    time.Now().UTC(),
			}

			err := tx.Transaction(context.Background(), func(txCtx context.Context) error {
				if writeErr := grabRepo.Create(txCtx, rec); writeErr != nil {
					return writeErr
				}
				// Force failure on the second write — simulates cooldown write error
				// mid-transaction (e.g. constraint violation, DB error).
				return cooldownRepo.Set(txCtx, cooldown.Cooldown{
					Scope:     cooldown.ScopeSeries,
					Key:       "main:122:2",
					ExpiresAt: time.Now().UTC().Add(time.Hour),
					Reason:    "test",
				})
			})

			require.Error(t, err, "transaction must propagate the forced error")
			assert.True(t, errors.Is(err, errForcedMidTx), "error must wrap the forced error")

			// The grab row must NOT exist — the transaction must have rolled back.
			var count int64
			db.Table("grab_records").Where("id = ?", rec.ID.String()).Count(&count)
			assert.Equal(t, int64(0), count, "grabs.Create must be rolled back when cooldowns.Set fails mid-transaction")
		})
	}
}

// failingCooldownRepo wraps watchdogpersistence.CooldownRepository and returns errForcedMidTx from Set.
type failingCooldownRepo struct {
	inner *watchdogpersistence.CooldownRepository
}

func (r *failingCooldownRepo) Set(_ context.Context, _ cooldown.Cooldown) error {
	return errForcedMidTx
}
