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

// Throwaway (W18-16 impl verification): drive golang-migrate to version 34 (up),
// step back to 33 (down 000034), then forward to 34 again against the embedded
// SQLite migration set — proving 000034 down reverses the up cleanly (drops the
// skeleton_synced_at column) and re-applies. Uses an in-memory SQLite DB.
func TestMigration000034_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(34); err != nil {
		t.Fatalf("migrate up to 34: %v", err)
	}
	if !hasColumn(t, sqlDB, "skeleton_synced_at") {
		t.Fatalf("skeleton_synced_at missing after up to 34")
	}
	if err := m.Migrate(33); err != nil {
		t.Fatalf("migrate down to 33: %v", err)
	}
	if hasColumn(t, sqlDB, "skeleton_synced_at") {
		t.Fatalf("skeleton_synced_at still present after down to 33 — down did not reverse")
	}
	if err := m.Migrate(34); err != nil {
		t.Fatalf("migrate up to 34 again: %v", err)
	}
	if !hasColumn(t, sqlDB, "skeleton_synced_at") {
		t.Fatalf("skeleton_synced_at missing after re-up to 34")
	}
}

func hasColumn(t *testing.T, db *sql.DB, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info('series')`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == col {
			return true
		}
	}
	return false
}
