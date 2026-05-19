package database

import (
	"fmt"

	"gorm.io/gorm"
)

func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&ScanRunModel{},
		&DecisionModel{},
	); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	return nil
}
