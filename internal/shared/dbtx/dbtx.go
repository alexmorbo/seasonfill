// Package dbtx hosts the shared transaction-context plumbing that lets
// every catalog repository participate in a single gorm transaction
// opened by ports.Transactor. The key shape (txKey{}) lives in this
// kernel package so cross-context repos (e.g. internal/grab/persistence
// after story 431, the legacy infrastructure/database/repositories tree
// pending later A-1 stories) all use the SAME private type — without
// it, a tx opened by one half would be invisible to the other.
//
// Story 431 (A-1-5) extracted this out of
// infrastructure/database/repositories/tx.go when the grab repository
// graduated into internal/grab/persistence. The catalog repositories
// retained an unexported dbFromContext wrapper that delegates here; new
// per-context persistence packages (grab, media, …) call DBFromContext
// directly.
//
// Future stories may relocate more of the transactor surface here, but
// the minimum kernel needed for cross-context atomic writes is:
//
//   - txKey  — the shared private context key (unexported is fine; the
//     key is opaque, only the helpers below touch it)
//   - WithTx — derives a tx-scoped context
//   - DBFromContext — pulls the tx-scoped *gorm.DB out, falling back to
//     the supplied default when no transaction is in scope
package dbtx

import (
	"context"

	"gorm.io/gorm"
)

type txKey struct{}

// WithTx returns a context carrying the tx-scoped DB handle. Used by
// the catalog GormTransactor.Transaction implementation to thread the
// active gorm session through to every repo invoked by the work fn.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// DBFromContext returns the tx-scoped *gorm.DB if present in ctx,
// otherwise def. Every repository's read/write helper funnels through
// this so a Transactor.Transaction(ctx, fn) call routes every nested
// repo write to the same connection.
func DBFromContext(ctx context.Context, def *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok && tx != nil {
		return tx
	}
	return def
}
