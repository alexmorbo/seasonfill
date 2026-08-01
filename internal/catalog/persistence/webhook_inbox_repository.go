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

// WebhookInboxRepository persists the webhook_inbox table (ADR 0005,
// migration 000042). It is the durable inbox for raw Sonarr webhook
// events. Insert participates in a caller-opened
// ports.Transactor.Transaction via dbFromContext; the claim/settle
// methods run standalone from the drainer (E3).
//
// Compile-time proof this satisfies the port:
var _ ports.WebhookInboxRepository = (*WebhookInboxRepository)(nil)

type WebhookInboxRepository struct {
	db *gorm.DB
}

// NewWebhookInboxRepository constructs a repository over the given gorm
// handle. Stateless — safe to share across goroutines.
func NewWebhookInboxRepository(db *gorm.DB) *WebhookInboxRepository {
	return &WebhookInboxRepository{db: db}
}

// Insert enqueues a pending row. Routes through dbFromContext so a
// caller-opened Transactor.Transaction makes the enqueue atomic. Stamps
// CreatedAt=now() when zero and defaults Status to pending / Attempts to 0.
func (r *WebhookInboxRepository) Insert(ctx context.Context, row ports.WebhookInboxRow) error {
	if row.InstanceName == "" {
		return fmt.Errorf("insert webhook inbox: instance_name must be non-empty")
	}
	if row.EventType == "" {
		return fmt.Errorf("insert webhook inbox: event_type must be non-empty")
	}
	if len(row.Payload) == 0 {
		return fmt.Errorf("insert webhook inbox: payload must be non-empty")
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	status := row.Status
	if status == "" {
		status = ports.WebhookInboxStatusPending
	}
	m := database.WebhookInboxModel{
		InstanceName:  row.InstanceName,
		EventType:     row.EventType,
		Payload:       datatypes.JSON(row.Payload),
		Status:        status,
		Attempts:      row.Attempts,
		NextAttemptAt: row.NextAttemptAt,
		LeaseUntil:    row.LeaseUntil,
		LastError:     row.LastError,
		CreatedAt:     row.CreatedAt,
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("insert webhook inbox: %w", err)
	}
	return nil
}

// ClaimDue atomically claims up to `limit` due-pending rows FIFO by
// created_at. Portable two-step conditional UPDATE inside one transaction
// (F-07): (1) pluck due-pending ids FIFO, (2) conditional UPDATE guarded
// by status='pending', (3) read back the rows we flipped to processing.
// No SKIP LOCKED, no RETURNING. See story §DEVIATIONS 1-3.
func (r *WebhookInboxRepository) ClaimDue(ctx context.Context, now, leaseUntil time.Time, limit int) ([]ports.WebhookInboxRow, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("claim webhook inbox: limit must be positive")
	}

	base := dbFromContext(ctx, r.db).WithContext(ctx)
	var claimed []database.WebhookInboxModel

	err := base.Transaction(func(tx *gorm.DB) error {
		var ids []int64
		if err := tx.Model(&database.WebhookInboxModel{}).
			Where("status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)",
				ports.WebhookInboxStatusPending, now).
			Order("created_at ASC").
			Limit(limit).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := tx.Model(&database.WebhookInboxModel{}).
			Where("id IN ? AND status = ?", ids, ports.WebhookInboxStatusPending).
			Updates(map[string]any{
				"status":      ports.WebhookInboxStatusProcessing,
				"lease_until": leaseUntil,
			}).Error; err != nil {
			return err
		}
		return tx.
			Where("id IN ? AND status = ?", ids, ports.WebhookInboxStatusProcessing).
			Order("created_at ASC").
			Find(&claimed).Error
	})
	if err != nil {
		return nil, fmt.Errorf("claim webhook inbox: %w", err)
	}

	out := make([]ports.WebhookInboxRow, 0, len(claimed))
	for _, m := range claimed {
		out = append(out, toWebhookInboxRow(m))
	}
	return out, nil
}

// MarkSuccess deletes the row (delete-on-success). Idempotent — a missing
// row is not an error (a concurrent settle may have removed it).
func (r *WebhookInboxRepository) MarkSuccess(ctx context.Context, id int64) error {
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).
		Delete(&database.WebhookInboxModel{}).Error
	if err != nil {
		return fmt.Errorf("mark webhook inbox success: %w", err)
	}
	return nil
}

// MarkFailure records a retryable failure: attempts++, reschedule into
// next_attempt_at, set last_error, back to pending, clear lease.
// ErrNotFound on unknown id.
func (r *WebhookInboxRepository) MarkFailure(ctx context.Context, id int64, lastErr string, nextAttemptAt time.Time) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WebhookInboxModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"attempts":        gorm.Expr("attempts + 1"),
			"next_attempt_at": nextAttemptAt,
			"last_error":      lastErr,
			"status":          ports.WebhookInboxStatusPending,
			"lease_until":     nil,
		})
	if res.Error != nil {
		return fmt.Errorf("mark webhook inbox failure: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// MarkDead promotes the row to status=dead: set last_error, clear lease,
// row stays for forensics. ErrNotFound on unknown id.
func (r *WebhookInboxRepository) MarkDead(ctx context.Context, id int64, lastErr string) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WebhookInboxModel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      ports.WebhookInboxStatusDead,
			"last_error":  lastErr,
			"lease_until": nil,
		})
	if res.Error != nil {
		return fmt.Errorf("mark webhook inbox dead: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// ReclaimStale returns processing rows with an expired lease back to
// pending and clears their lease. attempts untouched. Returns count.
func (r *WebhookInboxRepository) ReclaimStale(ctx context.Context, now time.Time) (int64, error) {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WebhookInboxModel{}).
		Where("status = ? AND lease_until IS NOT NULL AND lease_until < ?",
			ports.WebhookInboxStatusProcessing, now).
		Updates(map[string]any{
			"status":      ports.WebhookInboxStatusPending,
			"lease_until": nil,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("reclaim stale webhook inbox: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// CountPending returns the number of rows in status=pending. Used by the
// E3 drainer's pending-depth gauge. NOT part of the WebhookInboxRepository
// port — a concrete-only helper so the E2 port stays frozen.
func (r *WebhookInboxRepository) CountPending(ctx context.Context) (int64, error) {
	var n int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WebhookInboxModel{}).
		Where("status = ?", ports.WebhookInboxStatusPending).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count pending webhook inbox: %w", err)
	}
	return n, nil
}

func toWebhookInboxRow(m database.WebhookInboxModel) ports.WebhookInboxRow {
	return ports.WebhookInboxRow{
		ID:            m.ID,
		InstanceName:  m.InstanceName,
		EventType:     m.EventType,
		Payload:       []byte(m.Payload),
		Status:        m.Status,
		Attempts:      m.Attempts,
		NextAttemptAt: m.NextAttemptAt,
		LeaseUntil:    m.LeaseUntil,
		LastError:     m.LastError,
		CreatedAt:     m.CreatedAt,
	}
}
