package persistence

import (
	"context"

	"gorm.io/gorm"
)

// txKey is the private context key used to thread a tx-scoped *gorm.DB
// through admin repository calls. It is intentionally a fresh local
// type (separate from the catalog-side repositories.txKey) so the
// admin context boundary is enforced at the type-system level: an
// admin repo will NEVER mistakenly join a transaction opened by a
// non-admin transactor, and vice versa.
//
// The admin context currently has no transactional flows of its own —
// admin_user / app_settings / quota_counter mutations are all single-
// statement upserts. The plumbing is kept for symmetry with the
// non-admin repos and so future cross-method admin flows (e.g. an
// atomic "rotate OIDC client secret + invalidate sessions" sequence)
// have a take-up path without re-introducing this scaffolding.
type txKey struct{}

// WithTx returns a context carrying the tx-scoped DB handle. Currently
// unused inside the admin context but kept exported so an admin
// transactor (if one is ever needed) can be added without churn here.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// dbFromContext returns the tx-scoped *gorm.DB if present, otherwise def.
func dbFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return def
}
