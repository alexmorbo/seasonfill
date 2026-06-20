package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// QuotaCounterRepository is the GORM-backed quota.QuotaCounter
// implementation. One row per (service_name, window_start) pair;
// Increment bumps via INSERT ... ON CONFLICT DO UPDATE so two
// concurrent callers on the same key cannot lose updates. Reset is
// a bulk DELETE used by the daily GC sweeper.
//
// The repository owns no business logic — it simply persists. The
// cap-vs-no-cap decision lives at the client layer (OMDbBudgetGuard,
// future TMDBQuotaGuard, etc.).
type QuotaCounterRepository struct {
	db *gorm.DB
}

func NewQuotaCounterRepository(db *gorm.DB) *QuotaCounterRepository {
	return &QuotaCounterRepository{db: db}
}

// Increment atomically bumps the (service, window) row by 1 (or
// inserts a fresh row with count=1 on first contact). Returns the
// post-update count. Style mirrors NoBetterCounterRepository.Increment.
//
// The post-increment re-read is unconditional because not every
// GORM driver populates `model.Count` with the new value on the
// UPSERT path; the extra SELECT costs ~1ms on Postgres and is
// negligible vs. the OMDb HTTP call this guards.
func (r *QuotaCounterRepository) Increment(ctx context.Context, service string, window time.Time) (int, error) {
	if service == "" {
		return 0, errors.New("quota_counter: service required")
	}
	winUTC := window.UTC()
	now := time.Now().UTC()

	insert := database.QuotaStateModel{
		ServiceName: service,
		WindowStart: winUTC,
		Count:       1,
		UpdatedAt:   now,
	}

	// ON CONFLICT (service_name, window_start) DO UPDATE
	// SET count = external_service_quota_state.count + 1,
	//     updated_at = excluded.updated_at.
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "service_name"},
			{Name: "window_start"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"count":      gorm.Expr("external_service_quota_state.count + 1"),
			"updated_at": now,
		}),
	}).Create(&insert)
	if res.Error != nil {
		return 0, fmt.Errorf("quota_counter increment: %w", res.Error)
	}

	// Re-read to return the post-update value.
	got, err := r.Get(ctx, service, winUTC)
	if err != nil {
		return 0, fmt.Errorf("quota_counter reload after increment: %w", err)
	}
	return got, nil
}

// Get returns the count for (service, window), or 0 when no row
// exists. Never returns ErrNotFound — a missing row is a fresh
// window for which nobody has Incremented yet, semantically zero.
func (r *QuotaCounterRepository) Get(ctx context.Context, service string, window time.Time) (int, error) {
	winUTC := window.UTC()
	var m database.QuotaStateModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("service_name = ? AND window_start = ?", service, winUTC).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("quota_counter get: %w", err)
	}
	return m.Count, nil
}

// Reset deletes every row whose window_start is strictly older than
// `before`. Returns the number of rows deleted for observability
// (the caller logs it).
func (r *QuotaCounterRepository) Reset(ctx context.Context, before time.Time) (int64, error) {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("window_start < ?", before.UTC()).
		Delete(&database.QuotaStateModel{})
	if res.Error != nil {
		return 0, fmt.Errorf("quota_counter reset: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// Compile-time assertion the repo satisfies the port.
var _ quota.QuotaCounter = (*QuotaCounterRepository)(nil)
