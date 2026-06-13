package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

const appSettingsID = uint(1)

// AppSettingsRepository is the GORM-backed CRUD surface for the
// singleton app_settings row (id=1). The row is seeded by the v36
// migration so Get never returns ErrNotFound on a healthy DB —
// callers may still expect it (defensive) but the happy path is
// always a hit.
type AppSettingsRepository struct {
	db *gorm.DB
}

func NewAppSettingsRepository(db *gorm.DB) *AppSettingsRepository {
	return &AppSettingsRepository{db: db}
}

// GetTimezone returns the stored IANA timezone name, or "" when the
// column is NULL (meaning "use env fallback"). Returns ErrNotFound
// when the singleton row is missing — that only happens if the v36
// seed INSERT was skipped, which would itself be a migration bug.
func (r *AppSettingsRepository) GetTimezone(ctx context.Context) (string, error) {
	var m database.AppSettingsModel
	err := r.db.WithContext(ctx).
		Where("id = ?", appSettingsID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ports.ErrNotFound
		}
		return "", fmt.Errorf("get app_settings: %w", err)
	}
	if m.Timezone == nil {
		return "", nil
	}
	return *m.Timezone, nil
}

// SetTimezone upserts the singleton row's timezone column. Passing
// an empty string CLEARS the override (column becomes NULL — env
// fallback resumes on the next Get). Caller is responsible for IANA
// validation; this method is a dumb writer.
func (r *AppSettingsRepository) SetTimezone(ctx context.Context, tzName string) error {
	now := time.Now().UTC()
	var tzPtr *string
	if tzName != "" {
		s := tzName
		tzPtr = &s
	}
	// Upsert via FirstOrCreate + explicit Update; mirrors the
	// runtime_config repo pattern. We don't use ON CONFLICT because
	// glebarez/sqlite has historic quirks with named constraints.
	var m database.AppSettingsModel
	err := r.db.WithContext(ctx).
		Where("id = ?", appSettingsID).
		Attrs(database.AppSettingsModel{
			ID: appSettingsID, Timezone: tzPtr, UpdatedAt: now,
		}).
		FirstOrCreate(&m).Error
	if err != nil {
		return fmt.Errorf("upsert app_settings: %w", err)
	}
	// FirstOrCreate populated the row if missing. If it already
	// existed, run an explicit Update so the new values land.
	return r.db.WithContext(ctx).
		Model(&database.AppSettingsModel{}).
		Where("id = ?", appSettingsID).
		Updates(map[string]any{
			"timezone":   tzPtr,
			"updated_at": now,
		}).Error
}
