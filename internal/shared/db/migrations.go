package database

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgdriver "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"

	sqlitedriver "github.com/alexmorbo/seasonfill/infrastructure/database/migratesqlite"
	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
)

// Migrate applies the 13 D-1 dual-dialect migrations to the database.
//
// Greenfield contract per ADR D2-revised-roadmap.md: the legacy
// v1..v44 chain + the stampBaselineIfNeeded probe are gone. Production
// drops the database at cutover (D-8) and the migrations apply cleanly
// from version 0. Idempotent — running twice is a no-op because
// golang-migrate respects the schema_migrations bookkeeping it owns.
func Migrate(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	dialect := db.Name()
	driver, err := newMigrateDriver(dialect, sqlDB)
	if err != nil {
		return err
	}

	var fsys fs.FS
	switch dialect {
	case "postgres":
		fsys, err = fs.Sub(migrations.Postgres, "postgres")
	case "sqlite":
		fsys, err = fs.Sub(migrations.SQLite, "sqlite")
	default:
		return fmt.Errorf("unsupported dialect: %s", dialect)
	}
	if err != nil {
		return fmt.Errorf("sub fs: %w", err)
	}

	src, err := iofs.New(fsys, ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, dialect, driver)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func newMigrateDriver(dialect string, sqlDB *sql.DB) (database.Driver, error) {
	switch dialect {
	case "postgres":
		d, err := pgdriver.WithInstance(sqlDB, &pgdriver.Config{})
		if err != nil {
			return nil, fmt.Errorf("postgres driver: %w", err)
		}
		return d, nil
	case "sqlite":
		d, err := sqlitedriver.WithInstance(sqlDB, &sqlitedriver.Config{})
		if err != nil {
			return nil, fmt.Errorf("sqlite driver: %w", err)
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unsupported dialect: %s", dialect)
	}
}
