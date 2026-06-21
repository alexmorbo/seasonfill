package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// WatchdogBlacklistRepository persists watchdog_blacklist rows. D-1 /
// 467b: composite PK on (instance_name, sonarr_series_id, season_number).
// No surrogate id — operations key on the triple.
type WatchdogBlacklistRepository struct {
	db *gorm.DB
}

func NewWatchdogBlacklistRepository(db *gorm.DB) *WatchdogBlacklistRepository {
	return &WatchdogBlacklistRepository{db: db}
}

// Find returns the blacklist row matching (instance, series, season)
// exactly. ports.ErrNotFound on miss.
func (r *WatchdogBlacklistRepository) Find(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
) (regrab.BlacklistEntry, error) {
	var m database.WatchdogBlacklistModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
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
// the row's Reason / Consecutive / BlacklistedAt / TTLUntil /
// ReleaseTitle are replaced. Caller-supplied CreatedAt defaults to
// time.Now().UTC() when zero.
func (r *WatchdogBlacklistRepository) Upsert(ctx context.Context, entry regrab.BlacklistEntry) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	model := database.WatchdogBlacklistModel{
		InstanceName:   entry.InstanceName,
		SonarrSeriesID: entry.SeriesID,
		SeasonNumber:   entry.SeasonNumber,
		ReleaseTitle:   entry.ReleaseTitle,
		Reason:         string(entry.Reason),
		Consecutive:    entry.Consecutive,
		BlacklistedAt:  entry.CreatedAt.UTC(),
		TTLUntil:       entry.TTLUntil,
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"release_title", "reason", "consecutive", "blacklisted_at", "ttl_until",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert blacklist: %w", res.Error)
	}
	return nil
}

// DeleteByTriple removes the parked row. ports.ErrNotFound on miss.
// Replaces legacy DeleteByID — the composite PK is the lookup key.
func (r *WatchdogBlacklistRepository) DeleteByTriple(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
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
// BlacklistedAt DESC so callers see newest-first.
func (r *WatchdogBlacklistRepository) ListByInstance(
	ctx context.Context, instance domain.InstanceName,
) ([]regrab.BlacklistEntry, error) {
	var models []database.WatchdogBlacklistModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ?", instance).
		Order("blacklisted_at DESC, sonarr_series_id DESC, season_number DESC").
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
		InstanceName: m.InstanceName,
		SeriesID:     m.SonarrSeriesID,
		SeasonNumber: m.SeasonNumber,
		ReleaseTitle: m.ReleaseTitle,
		Reason:       regrab.Reason(m.Reason),
		Consecutive:  m.Consecutive,
		CreatedAt:    m.BlacklistedAt,
		TTLUntil:     m.TTLUntil,
	}
}

var _ ports.WatchdogBlacklistRepository = (*WatchdogBlacklistRepository)(nil)
