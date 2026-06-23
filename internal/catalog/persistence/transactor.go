package persistence

import (
	"context"

	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// GormTransactor adapts gorm.DB to the application/ports.Transactor surface.
// The tx-scoped *gorm.DB is stashed in the derived context via WithTx so that
// repositories use dbFromContext to pick it up. This ensures all three writes
// in the M-7 success path (grabs.Create / cooldowns.Set / origins.Upsert)
// execute on the same connection/session on every SQL backend, including
// Postgres where individual repo calls would otherwise auto-commit.
//
// B-37 follow-up: the transaction is wrapped in a Postgres-deadlock retry
// budget (database.DefaultDeadlockRetryAttempts, jittered 50ms-to-1s
// exponential backoff). Series_worker and person_worker upsert the same
// `people` rows from independent transactions; with the atomic CASE upsert
// plus BatchUpsert sort-by-tmdb_id discipline on the burst paths, the
// deadlock rate drops to near zero — but the lock-order disagreement on
// applyEpisodeCredits' nested season×episode×guest walk is irreducible
// without architectural changes. TransactWithDeadlockRetry is the safety
// net: deadlock victim → retry the whole closure (every reachable repo
// write is an UPSERT, so the closure is idempotent). Non-deadlock errors
// are returned to the caller unchanged.
type GormTransactor struct {
	db *gorm.DB
}

func NewGormTransactor(db *gorm.DB) *GormTransactor {
	return &GormTransactor{db: db}
}

func (t *GormTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	scoped := t.db.WithContext(ctx)
	return database.TransactWithDeadlockRetry(scoped, database.DefaultDeadlockRetryAttempts, func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}
