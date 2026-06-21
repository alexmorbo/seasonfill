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

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// QbitSettingsRepository persists qbit_settings (D-6 / story 467c +
// migration 000018). Per-instance Watchdog config keyed on
// instance_name (TEXT PK, FK→sonarr_instance.name CASCADE).
type QbitSettingsRepository struct {
	db *gorm.DB
}

// NewQbitSettingsRepository wires the repo bound to db.
func NewQbitSettingsRepository(db *gorm.DB) *QbitSettingsRepository {
	return &QbitSettingsRepository{db: db}
}

// Upsert writes the per-instance qBit configuration. On conflict over
// the instance_name PK every mutable column updates; created_at stays
// sticky to the first insert.
func (r *QbitSettingsRepository) Upsert(ctx context.Context, rec ports.QbitSettingsRecord) error {
	if rec.InstanceName == "" {
		return fmt.Errorf("qbit_settings upsert: instance_name is required")
	}
	msgs := rec.CustomUnregisteredMsgs
	if msgs == nil {
		msgs = []string{}
	}
	msgsJSON, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("marshal custom_unregistered_msgs: %w", err)
	}
	var pubURL *string
	if rec.PublicURL != "" {
		s := rec.PublicURL
		pubURL = &s
	}
	now := rec.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	model := database.QbitSettingsModel{
		InstanceName:           rec.InstanceName,
		Enabled:                rec.Enabled,
		URL:                    rec.URL,
		Username:               rec.Username,
		PasswordEncrypted:      rec.PasswordEncrypted,
		Category:               rec.Category,
		PollIntervalMinutes:    rec.PollIntervalMinutes,
		RegrabCooldownHours:    rec.RegrabCooldownHours,
		MaxConsecutiveNoBetter: rec.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: datatypes.JSON(msgsJSON),
		PublicURL:              pubURL,
		CreatedAt:              createdAt,
		UpdatedAt:              now,
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "instance_name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"enabled", "url", "username", "password_encrypted",
			"category", "poll_interval_minutes", "regrab_cooldown_hours",
			"max_consecutive_no_better", "custom_unregistered_msgs",
			"qbit_public_url", "updated_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert qbit_settings: %w", res.Error)
	}
	return nil
}

// GetByInstance returns the settings row for an instance.
// ports.ErrNotFound joined with QbitSettingsNotFoundError on miss.
func (r *QbitSettingsRepository) GetByInstance(ctx context.Context, instance domain.InstanceName) (ports.QbitSettingsRecord, error) {
	var m database.QbitSettingsModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ?", instance).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.QbitSettingsRecord{}, errors.Join(
				&sharedErrors.QbitSettingsNotFoundError{InstanceName: instance},
				ports.ErrNotFound,
			)
		}
		return ports.QbitSettingsRecord{}, fmt.Errorf("get qbit_settings: %w", err)
	}
	return toQbitSettingsRecord(m)
}

// DeleteByInstance removes the settings row. ports.ErrNotFound joined
// with QbitSettingsNotFoundError when no row matches.
func (r *QbitSettingsRepository) DeleteByInstance(ctx context.Context, instance domain.InstanceName) error {
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ?", instance).
		Delete(&database.QbitSettingsModel{})
	if res.Error != nil {
		return fmt.Errorf("delete qbit_settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(
			&sharedErrors.QbitSettingsNotFoundError{InstanceName: instance},
			ports.ErrNotFound,
		)
	}
	return nil
}

// List returns every settings row ordered by instance_name ascending.
// Empty slice (never nil) when no rows.
func (r *QbitSettingsRepository) List(ctx context.Context) ([]ports.QbitSettingsRecord, error) {
	var models []database.QbitSettingsModel
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Order("instance_name ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list qbit_settings: %w", err)
	}
	out := make([]ports.QbitSettingsRecord, 0, len(models))
	for _, m := range models {
		rec, err := toQbitSettingsRecord(m)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// toQbitSettingsRecord projects the GORM model into the port record.
func toQbitSettingsRecord(m database.QbitSettingsModel) (ports.QbitSettingsRecord, error) {
	msgs := []string{}
	if len(m.CustomUnregisteredMsgs) > 0 {
		if err := json.Unmarshal(m.CustomUnregisteredMsgs, &msgs); err != nil {
			return ports.QbitSettingsRecord{}, fmt.Errorf("unmarshal custom_unregistered_msgs: %w", err)
		}
		if msgs == nil {
			msgs = []string{}
		}
	}
	pubURL := ""
	if m.PublicURL != nil {
		pubURL = *m.PublicURL
	}
	return ports.QbitSettingsRecord{
		InstanceName:           m.InstanceName,
		Enabled:                m.Enabled,
		URL:                    m.URL,
		Username:               m.Username,
		PasswordEncrypted:      m.PasswordEncrypted,
		Category:               m.Category,
		PollIntervalMinutes:    m.PollIntervalMinutes,
		RegrabCooldownHours:    m.RegrabCooldownHours,
		MaxConsecutiveNoBetter: m.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: msgs,
		PublicURL:              pubURL,
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
	}, nil
}

var _ ports.QbitSettingsRepository = (*QbitSettingsRepository)(nil)
