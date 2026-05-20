package repositories

import (
	"context"

	"gorm.io/gorm"
)

type txKey struct{}

// WithTx returns a context carrying the tx-scoped DB handle.
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
