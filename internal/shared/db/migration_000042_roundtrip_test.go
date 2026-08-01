package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000042_UpDownUp_SQLite drives golang-migrate to version 42 (up),
// steps back to 41 (down 000042), then forward to 42 again against the embedded
// SQLite migration set — proving 000042 down reverses the up cleanly (drops the
// webhook_inbox table + its partial pending index) and re-applies. Uses an
// in-memory SQLite DB.
func TestMigration000042_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(42); err != nil {
		t.Fatalf("migrate up to 42: %v", err)
	}
	if !hasTable(t, sqlDB, "webhook_inbox") {
		t.Fatalf("webhook_inbox table missing after up to 42")
	}

	if err := m.Migrate(41); err != nil {
		t.Fatalf("migrate down to 41: %v", err)
	}
	if hasTable(t, sqlDB, "webhook_inbox") {
		t.Fatalf("webhook_inbox still present after down to 41 — down did not reverse")
	}

	if err := m.Migrate(42); err != nil {
		t.Fatalf("migrate up to 42 again: %v", err)
	}
	if !hasTable(t, sqlDB, "webhook_inbox") {
		t.Fatalf("webhook_inbox missing after re-up to 42")
	}
}
