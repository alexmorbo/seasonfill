package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// dbFromContext returns the tx-scoped *gorm.DB if a Transactor opened one
// (so SetStatus + the outbox emit are atomic), otherwise def.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	return dbtx.DBFromContext(ctx, def)
}
