package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

type WatchdogBlacklistRepository struct {
	db *gorm.DB
}

func NewWatchdogBlacklistRepository(db *gorm.DB) *WatchdogBlacklistRepository {
	return &WatchdogBlacklistRepository{db: db}
}

// Find returns the blacklist row matching (instance, series, season)
// exactly. ports.ErrNotFound on miss.
func (r *WatchdogBlacklistRepository) Find(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int) (regrab.BlacklistEntry, error) {
	var m database.WatchdogBlacklistModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND series_id = ? AND season_number = ?", instanceID, seriesID, season).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return regrab.BlacklistEntry{}, ports.ErrNotFound
		}
		return regrab.BlacklistEntry{}, fmt.Errorf("find blacklist: %w", err)
	}
	return toBlacklistEntry(m), nil
}

// Upsert writes the row keyed on (instance, series, season). On conflict
// the row's Reason / Consecutive / CreatedAt / ExpiresAt are replaced.
func (r *WatchdogBlacklistRepository) Upsert(ctx context.Context, entry regrab.BlacklistEntry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	model := database.WatchdogBlacklistModel{
		InstanceID:   entry.InstanceID,
		SeriesID:     entry.SeriesID,
		SeasonNumber: entry.SeasonNumber,
		Reason:       string(entry.Reason),
		Consecutive:  entry.Consecutive,
		CreatedAt:    entry.CreatedAt,
		ExpiresAt:    entry.ExpiresAt,
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_id"},
			{Name: "series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"reason", "consecutive", "created_at", "expires_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert blacklist: %w", res.Error)
	}
	return nil
}

// DeleteByTriple removes the parked row. ports.ErrNotFound on miss.
func (r *WatchdogBlacklistRepository) DeleteByTriple(ctx context.Context, instanceID uint, seriesID domain.SonarrSeriesID, season int) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND series_id = ? AND season_number = ?", instanceID, seriesID, season).
		Delete(&database.WatchdogBlacklistModel{})
	if res.Error != nil {
		return fmt.Errorf("delete blacklist: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// ListByInstance returns every parked row for the instance ordered by
// CreatedAt DESC so callers see newest-first.
func (r *WatchdogBlacklistRepository) ListByInstance(ctx context.Context, instanceID uint) ([]regrab.BlacklistEntry, error) {
	var models []database.WatchdogBlacklistModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("created_at DESC, id DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list blacklist: %w", err)
	}
	out := make([]regrab.BlacklistEntry, 0, len(models))
	for _, m := range models {
		out = append(out, toBlacklistEntry(m))
	}
	return out, nil
}

func toBlacklistEntry(m database.WatchdogBlacklistModel) regrab.BlacklistEntry {
	return regrab.BlacklistEntry{
		ID:           m.ID,
		InstanceID:   m.InstanceID,
		SeriesID:     m.SeriesID,
		SeasonNumber: m.SeasonNumber,
		Reason:       regrab.Reason(m.Reason),
		Consecutive:  m.Consecutive,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
	}
}

var _ ports.WatchdogBlacklistRepository = (*WatchdogBlacklistRepository)(nil)
