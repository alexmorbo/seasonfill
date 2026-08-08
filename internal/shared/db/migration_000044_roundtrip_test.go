package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000044_UpDownUp_SQLite drives golang-migrate to version 44
// (creates torrent_action_audit), steps back to 43 (drops it), then forward
// to 44 again against the embedded SQLite set — proving 000044 down reverses
// the up cleanly and re-applies. In-memory SQLite. Reuses the package-local
// hasTable helper (migration_000035_roundtrip_test.go).
func TestMigration000044_UpDownUp_SQLite(t *testing.T) {
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

	const tbl = "torrent_action_audit"

	if err := m.Migrate(44); err != nil {
		t.Fatalf("migrate up to 44: %v", err)
	}
	if !hasTable(t, sqlDB, tbl) {
		t.Fatalf("%s missing after up to 44", tbl)
	}
	if err := m.Migrate(43); err != nil {
		t.Fatalf("migrate down to 43: %v", err)
	}
	if hasTable(t, sqlDB, tbl) {
		t.Fatalf("%s still present after down to 43", tbl)
	}
	if err := m.Migrate(44); err != nil {
		t.Fatalf("migrate up to 44 again: %v", err)
	}
	if !hasTable(t, sqlDB, tbl) {
		t.Fatalf("%s missing after re-up to 44", tbl)
	}
}
