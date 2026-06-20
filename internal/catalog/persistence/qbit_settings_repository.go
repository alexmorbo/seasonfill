package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

type QbitSettingsRepository struct {
	db *gorm.DB
}

func NewQbitSettingsRepository(db *gorm.DB) *QbitSettingsRepository {
	return &QbitSettingsRepository{db: db}
}

// Upsert writes or replaces the per-instance settings row. The DB unique
// index on instance_id is the conflict key. The repo serialises
// CustomUnregisteredMsgs to a JSON array (empty slice → "[]") so the
// column never holds NULL.
func (r *QbitSettingsRepository) Upsert(ctx context.Context, rec ports.QbitSettingsRecord) error {
	if rec.InstanceID == 0 {
		return fmt.Errorf("upsert qbit settings: instance_id must be non-zero")
	}
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	msgs := rec.CustomUnregisteredMsgs
	if msgs == nil {
		msgs = []string{}
	}
	raw, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("marshal custom_unregistered_msgs: %w", err)
	}

	model := database.InstanceQbitSettingsModel{
		InstanceID:             rec.InstanceID,
		Enabled:                rec.Enabled,
		URL:                    rec.URL,
		Username:               rec.Username,
		PasswordEncrypted:      rec.PasswordEncrypted,
		Category:               rec.Category,
		PollIntervalMinutes:    rec.PollIntervalMinutes,
		RegrabCooldownHours:    rec.RegrabCooldownHours,
		MaxConsecutiveNoBetter: rec.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: datatypes.JSON(raw),
		PublicURL:              publicURLPtr(rec.PublicURL),
		CreatedAt:              rec.CreatedAt,
		UpdatedAt:              rec.UpdatedAt,
	}

	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "instance_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled", "url", "username", "password_encrypted",
			"category", "poll_interval_minutes", "regrab_cooldown_hours",
			"max_consecutive_no_better", "custom_unregistered_msgs",
			"qbit_public_url",
			"updated_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert qbit settings: %w", res.Error)
	}
	return nil
}

// GetByInstance returns the settings row for the instance.
// ports.ErrNotFound on miss.
func (r *QbitSettingsRepository) GetByInstance(ctx context.Context, instanceID uint) (ports.QbitSettingsRecord, error) {
	var m database.InstanceQbitSettingsModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ?", instanceID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.QbitSettingsRecord{}, errors.Join(
				&sharedErrors.QbitSettingsNotFoundError{InstanceID: instanceID},
				ports.ErrNotFound,
			)
		}
		return ports.QbitSettingsRecord{}, fmt.Errorf("get qbit settings: %w", err)
	}
	return toQbitSettingsRecord(m)
}

// DeleteByInstance removes the row. ports.ErrNotFound on miss.
func (r *QbitSettingsRepository) DeleteByInstance(ctx context.Context, instanceID uint) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Delete(&database.InstanceQbitSettingsModel{})
	if res.Error != nil {
		return fmt.Errorf("delete qbit settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// List returns every settings row. Used by the regrab loop bootstrap
// (039g) to seed its per-instance sub-loops.
func (r *QbitSettingsRepository) List(ctx context.Context) ([]ports.QbitSettingsRecord, error) {
	var models []database.InstanceQbitSettingsModel
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list qbit settings: %w", err)
	}
	out := make([]ports.QbitSettingsRecord, 0, len(models))
	for _, m := range models {
		rec, err := toQbitSettingsRecord(m)
		if err != nil {
			return nil, fmt.Errorf("list qbit settings: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func toQbitSettingsRecord(m database.InstanceQbitSettingsModel) (ports.QbitSettingsRecord, error) {
	var msgs []string
	if len(m.CustomUnregisteredMsgs) > 0 {
		if err := json.Unmarshal(m.CustomUnregisteredMsgs, &msgs); err != nil {
			return ports.QbitSettingsRecord{}, fmt.Errorf("unmarshal custom_unregistered_msgs: %w", err)
		}
	}
	if msgs == nil {
		msgs = []string{}
	}
	publicURL := ""
	if m.PublicURL != nil {
		publicURL = *m.PublicURL
	}
	return ports.QbitSettingsRecord{
		ID:                     m.ID,
		InstanceID:             m.InstanceID,
		Enabled:                m.Enabled,
		URL:                    m.URL,
		Username:               m.Username,
		PasswordEncrypted:      m.PasswordEncrypted,
		Category:               m.Category,
		PollIntervalMinutes:    m.PollIntervalMinutes,
		RegrabCooldownHours:    m.RegrabCooldownHours,
		MaxConsecutiveNoBetter: m.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: msgs,
		PublicURL:              publicURL,
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
	}, nil
}

// publicURLPtr normalises empty strings to nil so the DB column stores
// NULL rather than the empty string. Trimming is the caller's job; this
// is a pure marshalling helper.
func publicURLPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ ports.QbitSettingsRepository = (*QbitSettingsRepository)(nil)
