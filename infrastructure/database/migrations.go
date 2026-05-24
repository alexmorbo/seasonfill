package database

import (
	"fmt"

	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&ScanRunModel{},
		&DecisionModel{},
		&GrabRecordModel{},
		&OriginReleaseModel{},
		&CooldownModel{},
		&AdminUserModel{},
		&RuntimeConfigModel{},
		&SonarrInstanceModel{},
		&InstanceSecretModel{},
	); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	return nil
}
