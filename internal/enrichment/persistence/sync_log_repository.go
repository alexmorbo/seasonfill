package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// SyncLogRepository persists the `sync_log` journal table (PRD §5.5,
// §5.6, §7.1). Single source of truth for TTL-staleness, degraded[]
// composition, and retry backoff. Workers Upsert one row per
// (entity, source) per fetch attempt; the dispatcher reads StaleScan
// (nightly background sweep) and RetryDue (error retries).
//
// Upsert is single-row (no batch path — each worker writes exactly
// one row per attempt). Attempts is caller-managed: the worker
// passes the new value; the repo does NOT do read-modify-write.
type SyncLogRepository struct {
	db *gorm.DB
}

func NewSyncLogRepository(db *gorm.DB) *SyncLogRepository {
	return &SyncLogRepository{db: db}
}

// Upsert writes one journal row by natural key
// (entity_type, entity_id, source). Empty Outcome is normalised to
// OutcomePending — same defensive default the schema has on the
// `outcome` column. Idempotent: re-running with the same payload
// mutates only updated_at.
func (r *SyncLogRepository) Upsert(ctx context.Context, entry enrichment.SyncLog) error {
	if !entry.EntityType.IsValid() {
		return fmt.Errorf("upsert sync_log: invalid entity_type %q", entry.EntityType)
	}
	if entry.EntityID == 0 {
		return fmt.Errorf("upsert sync_log: entity_id must be non-zero")
	}
	if !entry.Source.IsValid() {
		return fmt.Errorf("upsert sync_log: invalid source %q", entry.Source)
	}
	if entry.Outcome == "" {
		entry.Outcome = enrichment.OutcomePending
	}
	if !entry.Outcome.IsValid() {
		return fmt.Errorf("upsert sync_log: invalid outcome %q", entry.Outcome)
	}
	entry.UpdatedAt = time.Now().UTC()

	m := database.SyncLogModel{
		EntityType:    string(entry.EntityType),
		EntityID:      entry.EntityID,
		Source:        string(entry.Source),
		SyncedAt:      entry.SyncedAt,
		Outcome:       string(entry.Outcome),
		ErrorDetail:   entry.ErrorDetail,
		ETag:          entry.ETag,
		Attempts:      entry.Attempts,
		NextAttemptAt: entry.NextAttemptAt,
		DurationMs:    entry.DurationMs,
		UpdatedAt:     entry.UpdatedAt,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "entity_type"},
			{Name: "entity_id"},
			{Name: "source"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"synced_at", "outcome",
			"error_detail", "etag",
			"attempts", "next_attempt_at",
			"duration_ms",
			"updated_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert sync_log: %w", err)
	}
	return nil
}

// GetLastSync returns the journal row for
// (entity_type, entity_id, source). Returns ports.ErrNotFound on
// miss — used by the composer to compute "Source: TMDB · updated N
// ago" footers and by the dispatcher to surface "never synced" as a
// degraded[] entry.
func (r *SyncLogRepository) GetLastSync(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.SyncLog, error) {
	if !entityType.IsValid() {
		return enrichment.SyncLog{}, fmt.Errorf("get sync_log: invalid entity_type %q", entityType)
	}
	if !source.IsValid() {
		return enrichment.SyncLog{}, fmt.Errorf("get sync_log: invalid source %q", source)
	}
	var m database.SyncLogModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND source = ?",
			string(entityType), entityID, string(source)).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return enrichment.SyncLog{}, ports.ErrNotFound
		}
		return enrichment.SyncLog{}, fmt.Errorf("get sync_log: %w", err)
	}
	return toSyncLog(m), nil
}

// StaleScan returns up to `limit` journal rows for the given source
// where `outcome='ok' AND synced_at < cutoffTime`, ordered by
// `synced_at ASC` (oldest first). The nightly background sweep
// (PRD §5.5) uses this to enqueue entities for refresh; the TTL
// value comes from the domain TTL table (207), so callers compose
// `cutoffTime = now - TTL` at their layer.
//
// Restricting to `outcome='ok'` is what distinguishes "this entity
// needs a refresh" from "this entity has never synced" — the latter
// (no row at all, or `outcome='pending'`) is handled by the enqueue
// path that creates the row during initial scan, not here.
func (r *SyncLogRepository) StaleScan(ctx context.Context, source enrichment.Source, cutoffTime time.Time, limit int) ([]enrichment.SyncLog, error) {
	if !source.IsValid() {
		return nil, fmt.Errorf("stale scan sync_log: invalid source %q", source)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("stale scan sync_log: limit must be positive")
	}
	var models []database.SyncLogModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("source = ? AND outcome = ? AND synced_at < ?",
			string(source), string(enrichment.OutcomeOK), cutoffTime).
		Order("synced_at ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("stale scan sync_log: %w", err)
	}
	out := make([]enrichment.SyncLog, 0, len(models))
	for _, m := range models {
		out = append(out, toSyncLog(m))
	}
	return out, nil
}

// RetryDue returns up to `limit` journal rows for the given source
// where `outcome='error' AND next_attempt_at <= now`, ordered by
// `next_attempt_at ASC` (most-overdue first). The retry dispatcher
// (PRD §5.5) uses this to dequeue retryable failures. Reads the
// partial index `sync_log_retry WHERE outcome='error'` ON
// `(source, next_attempt_at)`.
func (r *SyncLogRepository) RetryDue(ctx context.Context, source enrichment.Source, now time.Time, limit int) ([]enrichment.SyncLog, error) {
	if !source.IsValid() {
		return nil, fmt.Errorf("retry due sync_log: invalid source %q", source)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("retry due sync_log: limit must be positive")
	}
	var models []database.SyncLogModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("source = ? AND outcome = ? AND next_attempt_at <= ?",
			string(source), string(enrichment.OutcomeError), now).
		Order("next_attempt_at ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("retry due sync_log: %w", err)
	}
	out := make([]enrichment.SyncLog, 0, len(models))
	for _, m := range models {
		out = append(out, toSyncLog(m))
	}
	return out, nil
}

func toSyncLog(m database.SyncLogModel) enrichment.SyncLog {
	return enrichment.SyncLog{
		EntityType:    enrichment.EntityType(m.EntityType),
		EntityID:      m.EntityID,
		Source:        enrichment.Source(m.Source),
		SyncedAt:      m.SyncedAt,
		Outcome:       enrichment.Outcome(m.Outcome),
		ErrorDetail:   m.ErrorDetail,
		ETag:          m.ETag,
		Attempts:      m.Attempts,
		NextAttemptAt: m.NextAttemptAt,
		DurationMs:    m.DurationMs,
		UpdatedAt:     m.UpdatedAt,
	}
}
