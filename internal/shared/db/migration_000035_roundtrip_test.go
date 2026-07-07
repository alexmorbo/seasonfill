package database

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// Throwaway (Story 1083 impl verification): drive golang-migrate to version 35
// (up), step back to 34 (down 000035), then forward to 35 again against the
// embedded SQLite migration set — proving 000035 down reverses the up cleanly
// (drops the people_texts table) and re-applies. In-memory SQLite.
func TestMigration000035_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(35); err != nil {
		t.Fatalf("migrate up to 35: %v", err)
	}
	if !hasTable(t, sqlDB, "people_texts") {
		t.Fatalf("people_texts missing after up to 35")
	}
	if err := m.Migrate(34); err != nil {
		t.Fatalf("migrate down to 34: %v", err)
	}
	if hasTable(t, sqlDB, "people_texts") {
		t.Fatalf("people_texts still present after down to 34 — down did not reverse")
	}
	if err := m.Migrate(35); err != nil {
		t.Fatalf("migrate up to 35 again: %v", err)
	}
	if !hasTable(t, sqlDB, "people_texts") {
		t.Fatalf("people_texts missing after re-up to 35")
	}
}

func hasTable(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	var name string
	err := db.QueryRowContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("sqlite_master query: %v", err)
	}
	return name == table
}
