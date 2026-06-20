package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// WithTx returns a context carrying the tx-scoped DB handle. Thin
// re-export of the kernel helper so the catalog-persistence repos that
// graduated from infrastructure/database/repositories during story 453
// (A-1-27) keep their dbFromContext call sites unchanged.
//
// The GormTransactor (transactor.go) calls this name from its
// Transaction implementation; the shared dbtx key is the same one used
// by every other context's persistence package so cross-context repos
// participate in the same transaction.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return dbtx.WithTx(ctx, tx)
}

// dbFromContext returns the tx-scoped *gorm.DB if present, otherwise
// def. Internal alias kept so the repos moved from
// infrastructure/database/repositories during story 453 keep their
// dbFromContext call sites unchanged.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	return dbtx.DBFromContext(ctx, def)
}
