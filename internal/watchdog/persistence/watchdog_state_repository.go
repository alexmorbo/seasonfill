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
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// WatchdogStateRepository implements ports.WatchdogStateRepository on
// top of the D-1 watchdog_state table (composite PK on instance_name,
// sonarr_series_id, season_number). Replaces the legacy
// NoBetterCounterRepository — attempt_count is the consecutive counter;
// cooldown_until + last_error are new D-1 columns.
type WatchdogStateRepository struct {
	db *gorm.DB
}

func NewWatchdogStateRepository(db *gorm.DB) *WatchdogStateRepository {
	return &WatchdogStateRepository{db: db}
}

// Get returns the row for the triple. ports.ErrNotFound on miss — the
// regrab use case treats that as "fresh triple, call Increment".
func (r *WatchdogStateRepository) Get(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
) (regrab.WatchdogState, error) {
	var m database.WatchdogStateModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return regrab.WatchdogState{}, ports.ErrNotFound
		}
		return regrab.WatchdogState{}, fmt.Errorf("get watchdog_state: %w", err)
	}
	return toWatchdogState(m), nil
}

// Increment atomically bumps attempt_count by 1 (or inserts a row with
// attempt_count=1 on first contact). Uses INSERT ... ON CONFLICT DO
// UPDATE so two concurrent regrab polls on the same triple cannot
// collide on a SELECT-then-UPDATE race. Returns the post-update row.
//
// The gorm.Expr increment is the atomic primitive verified by
// TestWatchdogState_Increment_AtomicConcurrent — Postgres serialises
// at the row level under ON CONFLICT, SQLite's in-memory write lock
// serialises every write.
func (r *WatchdogStateRepository) Increment(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
	now time.Time,
) (regrab.WatchdogState, error) {
	if instance == "" || seriesID <= 0 || season < 0 {
		return regrab.WatchdogState{}, fmt.Errorf(
			"invalid triple: instance=%q series=%d season=%d", instance, seriesID, season)
	}
	utc := now.UTC()
	insert := database.WatchdogStateModel{
		InstanceName:   instance,
		SonarrSeriesID: seriesID,
		SeasonNumber:   season,
		AttemptCount:   1,
		LastAttemptAt:  utc,
		UpdatedAt:      utc,
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"attempt_count":   gorm.Expr("watchdog_state.attempt_count + 1"),
			"last_attempt_at": utc,
			"updated_at":      utc,
		}),
	}).Create(&insert)
	if res.Error != nil {
		return regrab.WatchdogState{}, fmt.Errorf("increment watchdog_state: %w", res.Error)
	}
	// Re-read the row to return the post-update state. The UPSERT path
	// doesn't reliably populate insert.AttemptCount with the post-update
	// value across all drivers.
	got, err := r.Get(ctx, instance, seriesID, season)
	if err != nil {
		return regrab.WatchdogState{}, fmt.Errorf("reload after increment: %w", err)
	}
	return got, nil
}

// Reset zeros attempt_count, stamps last_attempt_at + updated_at,
// preserves cooldown_until and last_error. ports.ErrNotFound on miss.
func (r *WatchdogStateRepository) Reset(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
	now time.Time,
) error {
	utc := now.UTC()
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogStateModel{}).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
		Updates(map[string]any{
			"attempt_count":   0,
			"last_attempt_at": utc,
			"updated_at":      utc,
		})
	if res.Error != nil {
		return fmt.Errorf("reset watchdog_state: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// SetCooldownUntil writes the cooldown_until stamp. UPDATE-only — the
// row must exist (caller calls Increment first). ports.ErrNotFound on miss.
func (r *WatchdogStateRepository) SetCooldownUntil(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
	until time.Time,
) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogStateModel{}).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
		Updates(map[string]any{
			"cooldown_until": until.UTC(),
			"updated_at":     time.Now().UTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("set cooldown_until: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// SetLastError writes the last_error column on the row. ports.ErrNotFound
// on miss.
func (r *WatchdogStateRepository) SetLastError(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
	errMsg string,
) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogStateModel{}).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
		Updates(map[string]any{
			"last_error": errMsg,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("set last_error: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// DeleteByTriple removes the row entirely. ports.ErrNotFound on miss.
func (r *WatchdogStateRepository) DeleteByTriple(
	ctx context.Context,
	instance domain.InstanceName,
	seriesID domain.SonarrSeriesID,
	season int,
) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ? AND season_number = ?",
			instance, seriesID, season).
		Delete(&database.WatchdogStateModel{})
	if res.Error != nil {
		return fmt.Errorf("delete watchdog_state: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// ListByInstance returns every state row for the instance, ordered by
// updated_at DESC.
func (r *WatchdogStateRepository) ListByInstance(
	ctx context.Context,
	instance domain.InstanceName,
) ([]regrab.WatchdogState, error) {
	var models []database.WatchdogStateModel
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ?", instance).
		Order("updated_at DESC, sonarr_series_id DESC, season_number DESC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list watchdog_state by instance: %w", err)
	}
	out := make([]regrab.WatchdogState, 0, len(models))
	for _, m := range models {
		out = append(out, toWatchdogState(m))
	}
	return out, nil
}

// ListCooldownsDue returns rows whose cooldown_until <= now and
// non-NULL, ordered by cooldown_until ASC. Powers the regrab loop scheduler.
func (r *WatchdogStateRepository) ListCooldownsDue(
	ctx context.Context,
	instance domain.InstanceName,
	now time.Time,
) ([]regrab.WatchdogState, error) {
	utc := now.UTC()
	var models []database.WatchdogStateModel
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND cooldown_until IS NOT NULL AND cooldown_until <= ?",
			instance, utc).
		Order("cooldown_until ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list cooldowns due: %w", err)
	}
	out := make([]regrab.WatchdogState, 0, len(models))
	for _, m := range models {
		out = append(out, toWatchdogState(m))
	}
	return out, nil
}

func toWatchdogState(m database.WatchdogStateModel) regrab.WatchdogState {
	return regrab.WatchdogState{
		InstanceName:   m.InstanceName,
		SonarrSeriesID: m.SonarrSeriesID,
		SeasonNumber:   m.SeasonNumber,
		AttemptCount:   m.AttemptCount,
		LastAttemptAt:  m.LastAttemptAt,
		CooldownUntil:  m.CooldownUntil,
		LastError:      m.LastError,
		UpdatedAt:      m.UpdatedAt,
	}
}

var _ ports.WatchdogStateRepository = (*WatchdogStateRepository)(nil)
