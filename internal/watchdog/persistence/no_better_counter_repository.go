package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// NoBetterCounterModel mirrors regrab_no_better_counter row-for-row.
// Lives in this file rather than infrastructure/database/models.go
// because no other repository reads/writes the table — keeping the
// type and the repo together reduces blast radius if the schema
// changes.
type NoBetterCounterModel struct {
	ID           uint                  `gorm:"primaryKey"`
	InstanceID   uint                  `gorm:"uniqueIndex:idx_regrab_no_better_counter_triple,priority:1;index:idx_regrab_no_better_counter_instance_id"`
	SeriesID     domain.SonarrSeriesID `gorm:"uniqueIndex:idx_regrab_no_better_counter_triple,priority:2"`
	SeasonNumber int                   `gorm:"uniqueIndex:idx_regrab_no_better_counter_triple,priority:3"`
	Consecutive  int                   `gorm:"not null;default:0"`
	LastSeenAt   time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (NoBetterCounterModel) TableName() string { return "regrab_no_better_counter" }

type NoBetterCounterRepository struct {
	db *gorm.DB
}

func NewNoBetterCounterRepository(db *gorm.DB) *NoBetterCounterRepository {
	return &NoBetterCounterRepository{db: db}
}

// Get returns the counter row for the triple. ports.ErrNotFound on
// miss — the use case treats that as "fresh triple, call Increment".
func (r *NoBetterCounterRepository) Get(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int) (regrab.NoBetterCounter, error) {
	var m NoBetterCounterModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND series_id = ? AND season_number = ?", instanceID, seriesID, season).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return regrab.NoBetterCounter{}, ports.ErrNotFound
		}
		return regrab.NoBetterCounter{}, fmt.Errorf("get no_better_counter: %w", err)
	}
	return toNoBetterCounter(m), nil
}

// Increment atomically bumps consecutive by 1 (or inserts a fresh row
// with consecutive=1). Uses INSERT ... ON CONFLICT DO UPDATE so two
// concurrent regrab polls on the same triple cannot collide on a
// SELECT-then-UPDATE race. Returns the post-update counter so callers
// can decide whether to escalate.
func (r *NoBetterCounterRepository) Increment(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int, now time.Time) (regrab.NoBetterCounter, error) {
	if instanceID == 0 || seriesID <= 0 || season < 0 {
		return regrab.NoBetterCounter{}, fmt.Errorf("invalid triple: instance=%d series=%d season=%d", instanceID, seriesID, season)
	}
	utc := now.UTC()

	// First-contact row payload — used when the conflict path doesn't fire.
	insert := NoBetterCounterModel{
		InstanceID:   instanceID,
		SeriesID:     seriesID,
		SeasonNumber: season,
		Consecutive:  1,
		LastSeenAt:   utc,
		CreatedAt:    utc,
		UpdatedAt:    utc,
	}

	// ON CONFLICT (instance_id, series_id, season_number) DO UPDATE
	// SET consecutive = regrab_no_better_counter.consecutive + 1,
	//     last_seen_at = excluded.last_seen_at,
	//     updated_at = excluded.updated_at.
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_id"},
			{Name: "series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"consecutive":  gorm.Expr("regrab_no_better_counter.consecutive + 1"),
			"last_seen_at": utc,
			"updated_at":   utc,
		}),
	}).Create(&insert)
	if res.Error != nil {
		return regrab.NoBetterCounter{}, fmt.Errorf("increment no_better_counter: %w", res.Error)
	}

	// Re-read the row to return the post-update state. The UPSERT path
	// doesn't reliably populate insert.Consecutive with the post-update
	// value across all drivers.
	got, err := r.Get(ctx, instanceID, seriesID, season)
	if err != nil {
		return regrab.NoBetterCounter{}, fmt.Errorf("reload after increment: %w", err)
	}
	return got, nil
}

// Reset zeros the row's consecutive counter. ports.ErrNotFound on miss.
func (r *NoBetterCounterRepository) Reset(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int, now time.Time) error {
	utc := now.UTC()
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&NoBetterCounterModel{}).
		Where("instance_id = ? AND series_id = ? AND season_number = ?", instanceID, seriesID, season).
		Updates(map[string]any{
			"consecutive": 0,
			"updated_at":  utc,
		})
	if res.Error != nil {
		return fmt.Errorf("reset no_better_counter: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// DeleteByTriple removes the row entirely.
func (r *NoBetterCounterRepository) DeleteByTriple(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND series_id = ? AND season_number = ?", instanceID, seriesID, season).
		Delete(&NoBetterCounterModel{})
	if res.Error != nil {
		return fmt.Errorf("delete no_better_counter: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func toNoBetterCounter(m NoBetterCounterModel) regrab.NoBetterCounter {
	return regrab.NoBetterCounter{
		ID:           m.ID,
		InstanceID:   m.InstanceID,
		SeriesID:     m.SeriesID,
		SeasonNumber: m.SeasonNumber,
		Consecutive:  m.Consecutive,
		LastSeenAt:   m.LastSeenAt,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

var _ ports.NoBetterCounterRepository = (*NoBetterCounterRepository)(nil)
