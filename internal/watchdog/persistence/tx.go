package persistence

import (
	"context"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// WithTx returns a context carrying the tx-scoped DB handle. Thin
// re-export of the kernel helper so the watchdog-persistence repos
// participate in catalog GormTransactor transactions started in the
// catalog persistence package — they share the same dbtx key from
// internal/shared/dbtx.
//
// Story 453 (A-1-27) introduced this shim when the watchdog blacklist
// + seasons repos moved here from infrastructure/database/repositories.
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
