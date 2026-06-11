package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
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
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
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
	err := dbFromContext(ctx, r.db).WithContext(ctx).First(&model, "scope = ? AND key = ?", string(scope), key).Error
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
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
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

func (r *CooldownRepository) Sweep(ctx context.Context, now time.Time) (int64, error) {
	res := dbFromContext(ctx, r.db).WithContext(ctx).Where("expires_at <= ?", now).Delete(&database.CooldownModel{})
	if res.Error != nil {
		return 0, fmt.Errorf("sweep cooldowns: %w", res.Error)
	}
	return res.RowsAffected, nil
}

var _ ports.CooldownRepository = (*CooldownRepository)(nil)
