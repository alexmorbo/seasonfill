package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000064_UpDownUp_SQLite drives golang-migrate to 64 (up), back to
// 63 (down 000064), then forward to 64 again — proving the ADR-0023 A3b
// default_minimum_availability column is added to BOTH settings tables and that
// the down cleanly reverses and re-applies.
func TestMigration000064_UpDownUp_SQLite(t *testing.T) {
	gdb, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}

	fsys, err := fs.Sub(migrations.SQLite, "sqlite")
	if err != nil {
		t.Fatalf("sub fs: %v", err)
	}
	src, err := iofs.New(fsys, ".")
	if err != nil {
		t.Fatalf("iofs: %v", err)
	}
	drv, err := newMigrateDriver("sqlite", sqlDB)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", drv)
	if err != nil {
		t.Fatalf("migrate instance: %v", err)
	}

	const col = "default_minimum_availability"
	tables := []string{"sonarr_instance_settings", "radarr_instance_settings"}

	if err := m.Migrate(64); err != nil {
		t.Fatalf("migrate up to 64: %v", err)
	}
	for _, tbl := range tables {
		if !hasSettingsColumn(t, sqlDB, tbl, col) {
			t.Fatalf("%s.%s missing after up to 64", tbl, col)
		}
	}

	if err := m.Migrate(63); err != nil {
		t.Fatalf("migrate down to 63: %v", err)
	}
	for _, tbl := range tables {
		if hasSettingsColumn(t, sqlDB, tbl, col) {
			t.Fatalf("%s.%s still present after down to 63 — down did not reverse", tbl, col)
		}
	}

	if err := m.Migrate(64); err != nil {
		t.Fatalf("migrate up to 64 again: %v", err)
	}
	for _, tbl := range tables {
		if !hasSettingsColumn(t, sqlDB, tbl, col) {
			t.Fatalf("%s.%s missing after re-up to 64", tbl, col)
		}
	}
}
