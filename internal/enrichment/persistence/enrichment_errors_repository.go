package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// EnrichmentErrorsRepository persists the `enrichment_errors` table
// (D-1 migration 000008, PRD §4.4). Only failure rows live here —
// success is recorded on the canonical entity row's
// `enrichment_*_synced_at` column.
//
// Lifecycle per (entity_type, entity_id, source):
//   - First failure → RecordFailure inserts (attempts=1, first_seen_at=now).
//   - Subsequent failures → UPSERT bumps attempts + last_seen_at + next_attempt_at.
//   - Success → ClearOnSuccess deletes the row.
//   - Retry dispatcher → ListDueForRetry reads next_attempt_at <= now via
//     the partial index `enrichment_errors_next_attempt`.
type EnrichmentErrorsRepository struct {
	db *gorm.DB
}

// NewEnrichmentErrorsRepository constructs a repository over the given
// gorm handle. Stateless — safe to share across goroutines.
func NewEnrichmentErrorsRepository(db *gorm.DB) *EnrichmentErrorsRepository {
	return &EnrichmentErrorsRepository{db: db}
}

// RecordFailure inserts-or-updates an error row by natural key
// (entity_type, entity_id, source). On insert, first_seen_at and
// last_seen_at default to now (DB default); on update, only
// last_seen_at + attempts + next_attempt_at + last_error change.
func (r *EnrichmentErrorsRepository) RecordFailure(ctx context.Context, e enrichment.EnrichmentError) error {
	if !e.EntityType.IsValid() {
		return fmt.Errorf("record enrichment error: invalid entity_type %q", e.EntityType)
	}
	if e.EntityID == 0 {
		return fmt.Errorf("record enrichment error: entity_id must be non-zero")
	}
	if !e.Source.IsValid() {
		return fmt.Errorf("record enrichment error: invalid source %q", e.Source)
	}
	if e.LastError == "" {
		return fmt.Errorf("record enrichment error: last_error must be non-empty")
	}
	if e.Attempts <= 0 {
		e.Attempts = 1
	}
	now := time.Now().UTC()
	if e.FirstSeenAt.IsZero() {
		e.FirstSeenAt = now
	}
	if e.LastSeenAt.IsZero() {
		e.LastSeenAt = now
	}
	m := database.EnrichmentErrorModel{
		EntityType:    string(e.EntityType),
		EntityID:      e.EntityID,
		Source:        string(e.Source),
		LastError:     e.LastError,
		Attempts:      e.Attempts,
		FirstSeenAt:   e.FirstSeenAt,
		LastSeenAt:    e.LastSeenAt,
		NextAttemptAt: e.NextAttemptAt,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "entity_type"},
			{Name: "entity_id"},
			{Name: "source"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"last_error":      gorm.Expr("excluded.last_error"),
			"attempts":        gorm.Expr("excluded.attempts"),
			"last_seen_at":    gorm.Expr("excluded.last_seen_at"),
			"next_attempt_at": gorm.Expr("excluded.next_attempt_at"),
			// first_seen_at preserved across updates (NOT in DoUpdates).
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("record enrichment error: %w", err)
	}
	return nil
}

// ClearOnSuccess deletes the error row for (entity_type, entity_id, source).
// Idempotent: DELETE of zero rows is fine (the row may have been cleared
// by a concurrent worker; the row may never have existed for first-time
// successes).
func (r *EnrichmentErrorsRepository) ClearOnSuccess(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) error {
	if !entityType.IsValid() {
		return fmt.Errorf("clear enrichment error: invalid entity_type %q", entityType)
	}
	if !source.IsValid() {
		return fmt.Errorf("clear enrichment error: invalid source %q", source)
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND source = ?",
			string(entityType), entityID, string(source)).
		Delete(&database.EnrichmentErrorModel{}).Error
	if err != nil {
		return fmt.Errorf("clear enrichment error: %w", err)
	}
	return nil
}

// GetForEntity returns ALL error rows for (entity_type, entity_id),
// across all sources. Composer's degraded[] surface reads this then
// filters by source to build the per-source live-error flag (PRD §5.6).
// Returns empty slice (NOT nil) when no rows match; ports.ErrNotFound
// is NOT used here — "no errors" is the happy path, not a miss.
func (r *EnrichmentErrorsRepository) GetForEntity(ctx context.Context, entityType enrichment.EntityType, entityID int64) ([]enrichment.EnrichmentError, error) {
	if !entityType.IsValid() {
		return nil, fmt.Errorf("get enrichment errors: invalid entity_type %q", entityType)
	}
	var models []database.EnrichmentErrorModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?",
			string(entityType), entityID).
		Order("source ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("get enrichment errors: %w", err)
	}
	out := make([]enrichment.EnrichmentError, 0, len(models))
	for _, m := range models {
		out = append(out, toEnrichmentError(m))
	}
	return out, nil
}

// ListDueForRetry returns up to `limit` rows for `source` where
// next_attempt_at <= `now`, ordered by next_attempt_at ASC
// (most-overdue first). Hits the partial index
// `enrichment_errors_next_attempt WHERE next_attempt_at IS NOT NULL`.
//
// Sources scope: D-3 wires this for SourceTMDBSeries / SourceTMDBPerson /
// SourceOMDb. Per-season retry queue is out of scope (no per-season
// sync target — seasons enrich as a by-product of series sync).
func (r *EnrichmentErrorsRepository) ListDueForRetry(ctx context.Context, source enrichment.Source, now time.Time, limit int) ([]enrichment.EnrichmentError, error) {
	if !source.IsValid() {
		return nil, fmt.Errorf("list enrichment errors due: invalid source %q", source)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list enrichment errors due: limit must be positive")
	}
	var models []database.EnrichmentErrorModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("source = ? AND next_attempt_at IS NOT NULL AND next_attempt_at <= ?",
			string(source), now).
		Order("next_attempt_at ASC").
		Limit(limit).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list enrichment errors due: %w", err)
	}
	out := make([]enrichment.EnrichmentError, 0, len(models))
	for _, m := range models {
		out = append(out, toEnrichmentError(m))
	}
	return out, nil
}

// GetByEntitySource returns the single error row for
// (entity_type, entity_id, source) or ports.ErrNotFound. Used by the
// composer when it needs to check "is THIS specific source currently
// erroring out for this entity?" without pulling all sources.
func (r *EnrichmentErrorsRepository) GetByEntitySource(ctx context.Context, entityType enrichment.EntityType, entityID int64, source enrichment.Source) (enrichment.EnrichmentError, error) {
	if !entityType.IsValid() {
		return enrichment.EnrichmentError{}, fmt.Errorf("get enrichment error: invalid entity_type %q", entityType)
	}
	if !source.IsValid() {
		return enrichment.EnrichmentError{}, fmt.Errorf("get enrichment error: invalid source %q", source)
	}
	var m database.EnrichmentErrorModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND source = ?",
			string(entityType), entityID, string(source)).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return enrichment.EnrichmentError{}, ports.ErrNotFound
		}
		return enrichment.EnrichmentError{}, fmt.Errorf("get enrichment error: %w", err)
	}
	return toEnrichmentError(m), nil
}

func toEnrichmentError(m database.EnrichmentErrorModel) enrichment.EnrichmentError {
	return enrichment.EnrichmentError{
		ID:            m.ID,
		EntityType:    enrichment.EntityType(m.EntityType),
		EntityID:      m.EntityID,
		Source:        enrichment.Source(m.Source),
		LastError:     m.LastError,
		Attempts:      m.Attempts,
		FirstSeenAt:   m.FirstSeenAt,
		LastSeenAt:    m.LastSeenAt,
		NextAttemptAt: m.NextAttemptAt,
	}
}
