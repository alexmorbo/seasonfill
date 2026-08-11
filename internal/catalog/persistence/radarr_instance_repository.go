package persistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// RadarrInstanceRepository reads per-instance Radarr settings + the shared
// arr_instance row (type='radarr'). Ф6-R-3 read-only slice; admin CRUD is R-6.
type RadarrInstanceRepository struct{ db *gorm.DB }

func NewRadarrInstanceRepository(db *gorm.DB) *RadarrInstanceRepository {
	return &RadarrInstanceRepository{db: db}
}

// GetSettings loads radarr_instance_settings for a type='radarr' instance.
// Returns ports.ErrNotFound when the arr_instance row is absent or not
// type='radarr'. Mirrors the settings read in SonarrInstanceRepository.GetByName.
// A present arr_instance row with an absent settings row yields the zero-value
// settings (with InstanceName populated) and no error — acceptable for R-3.
func (r *RadarrInstanceRepository) GetSettings(ctx context.Context, name string) (database.RadarrInstanceSettingsModel, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var inst database.SonarrInstanceModel // arr_instance row (shared table)
	if err := db.Where("name = ? AND type = ?", name, "radarr").First(&inst).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.RadarrInstanceSettingsModel{}, errors.Join(
				&sharedErrors.InstanceNotFoundError{Name: domain.InstanceName(name)},
				ports.ErrNotFound,
			)
		}
		return database.RadarrInstanceSettingsModel{}, fmt.Errorf("get radarr instance: %w", err)
	}

	var settings database.RadarrInstanceSettingsModel
	if err := db.Where("instance_name = ?", name).First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			settings.InstanceName = name
			return settings, nil
		}
		return database.RadarrInstanceSettingsModel{}, fmt.Errorf("get radarr instance settings: %w", err)
	}
	return settings, nil
}
