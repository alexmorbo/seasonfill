package repositories

import (
	"context"

	"gorm.io/gorm"
)

// GormTransactor adapts gorm.DB to the application/ports.Transactor surface.
// The tx-scoped *gorm.DB is stashed in the derived context via WithTx so that
// repositories use dbFromContext to pick it up. This ensures all three writes
// in the M-7 success path (grabs.Create / cooldowns.Set / origins.Upsert)
// execute on the same connection/session on every SQL backend, including
// Postgres where individual repo calls would otherwise auto-commit.
type GormTransactor struct {
	db *gorm.DB
}

func NewGormTransactor(db *gorm.DB) *GormTransactor {
	return &GormTransactor{db: db}
}

func (t *GormTransactor) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}
