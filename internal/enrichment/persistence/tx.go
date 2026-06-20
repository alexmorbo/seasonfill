package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// WithTx returns a context carrying the tx-scoped DB handle. Thin
// re-export of the kernel helper so the enrichment-persistence repos
// participate in catalog GormTransactor transactions started in the
// legacy infrastructure/database/repositories package — they share the
// same dbtx key from internal/shared/dbtx.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return dbtx.WithTx(ctx, tx)
}

// dbFromContext returns the tx-scoped *gorm.DB if present, otherwise
// def. Internal alias kept so the repos moved from
// infrastructure/database/repositories during story 437 keep their
// dbFromContext call sites unchanged. Future stories may convert them
// to dbtx.DBFromContext directly.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	return dbtx.DBFromContext(ctx, def)
}
