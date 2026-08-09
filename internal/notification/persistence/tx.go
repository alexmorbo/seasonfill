package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// dbFromContext returns the tx-scoped *gorm.DB if present (so an Insert made
// inside a caller-opened Transactor.Transaction is atomic with the source
// write), otherwise def.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	return dbtx.DBFromContext(ctx, def)
}
