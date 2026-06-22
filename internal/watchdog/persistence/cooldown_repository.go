package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

type CooldownRepository struct {
	db *gorm.DB
}

func NewCooldownRepository(db *gorm.DB) *CooldownRepository {
	return &CooldownRepository{db: db}
}

// Set upserts a cooldown row. Later expiry wins over earlier on conflict.
// Defensively clamps Reason at cooldown.ReasonMaxBytes (story 118) so a
// stale binary running against the pre-migration `varchar(128)` schema
// still succeeds (the clamp keeps writes well under 128 bytes by virtue
// of being well under 512), and so a future caller adding raw input
// cannot blow up the column.
func (r *CooldownRepository) Set(ctx context.Context, c cooldown.Cooldown) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	model := database.CooldownModel{
		Scope:     string(c.Scope),
		Key:       c.Key,
		ExpiresAt: c.ExpiresAt,
		Reason:    cooldown.ClampReason(c.Reason),
		CreatedAt: c.CreatedAt,
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "scope"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"expires_at", "reason", "created_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert cooldown: %w", res.Error)
	}
	return nil
}

func (r *CooldownRepository) Get(ctx context.Context, scope cooldown.Scope, key string) (cooldown.Cooldown, bool, error) {
	var model database.CooldownModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).First(&model, "scope = ? AND key = ?", string(scope), key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return cooldown.Cooldown{}, false, nil
		}
		return cooldown.Cooldown{}, false, fmt.Errorf("get cooldown: %w", err)
	}
	return cooldown.Cooldown{
		Scope:     cooldown.Scope(model.Scope),
		Key:       model.Key,
		ExpiresAt: model.ExpiresAt,
		Reason:    model.Reason,
		CreatedAt: model.CreatedAt,
	}, true, nil
}

func (r *CooldownRepository) FilterActive(ctx context.Context, scope cooldown.Scope, keys []string, now time.Time) ([]cooldown.Cooldown, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	var models []database.CooldownModel
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("scope = ? AND key IN ? AND expires_at > ?", string(scope), keys, now).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("filter active cooldowns: %w", err)
	}
	out := make([]cooldown.Cooldown, 0, len(models))
	for _, m := range models {
		out = append(out, cooldown.Cooldown{
			Scope:     cooldown.Scope(m.Scope),
			Key:       m.Key,
			ExpiresAt: m.ExpiresAt,
			Reason:    m.Reason,
			CreatedAt: m.CreatedAt,
		})
	}
	return out, nil
}

// CountActiveByScope returns the total number of cooldown rows in
// scope whose expires_at > now. Used by the watchdog state collector
// to publish the aggregate seasonfill_watchdog_cooldown_pending gauge
// (across all instances). For per-instance counts use
// CountActiveByScopeGroupedByInstance.
func (r *CooldownRepository) CountActiveByScope(ctx context.Context, scope cooldown.Scope, now time.Time) (int, error) {
	var count int64
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.CooldownModel{}).
		Where("scope = ? AND expires_at > ?", string(scope), now).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count active cooldowns by scope: %w", err)
	}
	return int(count), nil
}

// CountActiveByScopeGroupedByInstance lists every active cooldown row
// in scope, parses the instance segment out of the key, and returns
// a per-instance count map. Instance segment extraction relies on the
// key shape from internal/watchdog/domain/cooldown/cooldown.go
// SeriesKey:
//
//	"<instance>:<series_id>:<season>"
//
// Keys that don't parse map to an empty-string instance entry — those
// are silently dropped from the returned map (callers iterate the
// map and skip empty keys defensively).
func (r *CooldownRepository) CountActiveByScopeGroupedByInstance(
	ctx context.Context, scope cooldown.Scope, now time.Time,
) (map[domain.InstanceName]int, error) {
	var rows []database.CooldownModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("scope = ? AND expires_at > ?", string(scope), now).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list active cooldowns by scope: %w", err)
	}
	out := make(map[domain.InstanceName]int, len(rows))
	for _, row := range rows {
		inst := extractInstanceFromCooldownKey(row.Key)
		if inst == "" {
			continue
		}
		out[inst]++
	}
	return out, nil
}

// extractInstanceFromCooldownKey parses the instance segment out of a
// cooldown key. The shape is documented in
// internal/watchdog/domain/cooldown/cooldown.go SeriesKey —
// "<instance>:<series_id>:<season>". Returns empty string when the
// key shape is unrecognised (no colon present).
func extractInstanceFromCooldownKey(key string) domain.InstanceName {
	prefix, _, ok := strings.Cut(key, ":")
	if !ok {
		return ""
	}
	return domain.InstanceName(prefix)
}

func (r *CooldownRepository) Sweep(ctx context.Context, now time.Time) (int64, error) {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Where("expires_at <= ?", now).Delete(&database.CooldownModel{})
	if res.Error != nil {
		return 0, fmt.Errorf("sweep cooldowns: %w", res.Error)
	}
	return res.RowsAffected, nil
}

var _ ports.CooldownRepository = (*CooldownRepository)(nil)
