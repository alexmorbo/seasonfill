package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	appta "github.com/alexmorbo/seasonfill/internal/torrentaction/app"
)

// AuditRepository writes torrent_action_audit rows. Satisfies
// app.AuditWriter. Stateless GORM wrapper (same shape as GrabRepository);
// honours an ambient transaction via dbtx.DBFromContext.
type AuditRepository struct {
	db *gorm.DB
}

// NewAuditRepository constructs the repository over the shared *gorm.DB.
func NewAuditRepository(db *gorm.DB) *AuditRepository {
	return &AuditRepository{db: db}
}

// Write inserts one audit row. ID is DB-assigned (autoincrement); the
// caller's AuditRecord carries the resolved actual InstanceName + result.
func (r *AuditRepository) Write(ctx context.Context, rec appta.AuditRecord) error {
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)
	m := database.TorrentActionAuditModel{
		InstanceName: string(rec.InstanceName),
		Hash:         rec.Hash,
		Action:       string(rec.Action),
		Actor:        rec.Actor,
		Result:       rec.Result,
		CreatedAt:    rec.CreatedAt,
	}
	if err := db.Create(&m).Error; err != nil {
		return fmt.Errorf("insert torrent_action_audit: %w", err)
	}
	return nil
}
