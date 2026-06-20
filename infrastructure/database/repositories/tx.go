package repositories

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// WithTx returns a context carrying the tx-scoped DB handle. Thin
// re-export of the kernel helper — the catalog GormTransactor calls
// this name from its Transaction implementation; story 431 (A-1-5)
// moved the txKey itself into internal/shared/dbtx so a grab repo in
// internal/grab/persistence can participate in the same transaction.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return dbtx.WithTx(ctx, tx)
}

// dbFromContext returns the tx-scoped *gorm.DB if present, otherwise
// def. Internal alias kept so the 42 catalog repos in this package
// don't need their dbFromContext call sites rewritten — story 431
// touched the minimum needed for the grab vertical-slice move. Future
// stories may convert them to dbtx.DBFromContext directly.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	return dbtx.DBFromContext(ctx, def)
}
