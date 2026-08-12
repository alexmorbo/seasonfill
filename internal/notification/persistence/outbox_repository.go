package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

var _ ports.OutboxRepository = (*OutboxRepository)(nil)

type OutboxRepository struct{ db *gorm.DB }

func NewOutboxRepository(db *gorm.DB) *OutboxRepository { return &OutboxRepository{db: db} }

// Insert enqueues a pending row, collapsing storms on dedup_key. The
// dedup-check + insert run in ONE (possibly caller-opened) tx-scoped handle
// so a concurrent duplicate cannot slip between the SELECT and the INSERT
// within the same connection; cross-connection races are acceptable for a
// best-effort notification (worst case = a second ping).
func (r *OutboxRepository) Insert(ctx context.Context, row ports.OutboxRow) error {
	if row.EventType == "" {
		return fmt.Errorf("insert notification outbox: event_type must be non-empty")
	}
	if len(row.Payload) == 0 {
		return fmt.Errorf("insert notification outbox: payload must be non-empty")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	if row.DedupKey != nil && *row.DedupKey != "" {
		var n int64
		if err := db.Model(&database.NotificationOutboxModel{}).
			Where("status = ? AND dedup_key = ?", ports.NotificationOutboxStatusPending, *row.DedupKey).
			Count(&n).Error; err != nil {
			return fmt.Errorf("insert notification outbox (dedup check): %w", err)
		}
		if n > 0 {
			return nil // storm-collapse no-op
		}
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	status := row.Status
	if status == "" {
		status = ports.NotificationOutboxStatusPending
	}
	uid := row.UserID
	if uid == 0 { // system/broadcast emit → seed admin (D-1 option A)
		// Resolved on the tx-scoped handle (db) so an Insert inside a
		// caller-opened Transactor.Transaction stays on the SAME connection.
		// The shared UserRepository deliberately never joins this tx (its own
		// private txKey) — using it here would need a second connection and
		// deadlock SQLite's single-conn pool. Same seed-admin idiom as mig-059.
		var admin database.UserModel
		if err := db.Where("role = ?", "admin").Order("id ASC").First(&admin).Error; err != nil {
			return fmt.Errorf("insert notification outbox (resolve seed admin): %w", err)
		}
		uid = int64(admin.ID)
	}
	m := database.NotificationOutboxModel{
		UserID:        uid,
		EventType:     row.EventType,
		Payload:       datatypes.JSON(row.Payload),
		Status:        status,
		Attempts:      row.Attempts,
		NextAttemptAt: row.NextAttemptAt,
		DedupKey:      row.DedupKey,
		CreatedAt:     row.CreatedAt,
	}
	if err := db.Create(&m).Error; err != nil {
		return fmt.Errorf("insert notification outbox: %w", err)
	}
	return nil
}

func (r *OutboxRepository) FetchDueBatch(ctx context.Context, now time.Time, limit int) ([]ports.OutboxRow, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("fetch notification outbox: limit must be positive")
	}
	var ms []database.NotificationOutboxModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)",
			ports.NotificationOutboxStatusPending, now).
		Order("created_at ASC").
		Limit(limit).
		Find(&ms).Error; err != nil {
		return nil, fmt.Errorf("fetch notification outbox: %w", err)
	}
	out := make([]ports.OutboxRow, 0, len(ms))
	for _, m := range ms {
		out = append(out, toOutboxRow(m))
	}
	return out, nil
}

func (r *OutboxRepository) MarkSent(ctx context.Context, id int64) error {
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).Delete(&database.NotificationOutboxModel{}).Error; err != nil {
		return fmt.Errorf("mark notification outbox sent: %w", err)
	}
	return nil
}

func (r *OutboxRepository) Reschedule(ctx context.Context, id int64, nextAttemptAt time.Time) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.NotificationOutboxModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"attempts":        gorm.Expr("attempts + 1"),
			"next_attempt_at": nextAttemptAt,
			"status":          ports.NotificationOutboxStatusPending,
		})
	if res.Error != nil {
		return fmt.Errorf("reschedule notification outbox: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *OutboxRepository) MarkDead(ctx context.Context, id int64) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.NotificationOutboxModel{}).
		Where("id = ?", id).
		Update("status", ports.NotificationOutboxStatusDead)
	if res.Error != nil {
		return fmt.Errorf("mark notification outbox dead: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *OutboxRepository) CountPending(ctx context.Context) (int64, error) {
	var n int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.NotificationOutboxModel{}).
		Where("status = ?", ports.NotificationOutboxStatusPending).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count pending notification outbox: %w", err)
	}
	return n, nil
}

func toOutboxRow(m database.NotificationOutboxModel) ports.OutboxRow {
	return ports.OutboxRow{
		ID: m.ID, UserID: m.UserID, EventType: m.EventType, Payload: []byte(m.Payload),
		Status: m.Status, Attempts: m.Attempts, NextAttemptAt: m.NextAttemptAt,
		DedupKey: m.DedupKey, CreatedAt: m.CreatedAt,
	}
}
