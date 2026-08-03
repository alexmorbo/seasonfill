package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000043_UpDownUp_SQLite drives golang-migrate to version 43 (up),
// steps back to 42 (down 000043), then forward to 43 again against the embedded
// SQLite migration set — proving 000043 down reverses the up cleanly (drops the
// two sonarr_instance_settings default columns) and re-applies. Uses an
// in-memory SQLite DB.
func TestMigration000043_UpDownUp_SQLite(t *testing.T) {
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

	const tbl = "sonarr_instance_settings"

	if err := m.Migrate(43); err != nil {
		t.Fatalf("migrate up to 43: %v", err)
	}
	if !hasSettingsColumn(t, sqlDB, tbl, "default_quality_profile_id") {
		t.Fatalf("%s.default_quality_profile_id missing after up to 43", tbl)
	}
	if !hasSettingsColumn(t, sqlDB, tbl, "default_root_folder_path") {
		t.Fatalf("%s.default_root_folder_path missing after up to 43", tbl)
	}

	if err := m.Migrate(42); err != nil {
		t.Fatalf("migrate down to 42: %v", err)
	}
	if hasSettingsColumn(t, sqlDB, tbl, "default_quality_profile_id") {
		t.Fatalf("%s.default_quality_profile_id still present after down to 42 — down did not reverse", tbl)
	}
	if hasSettingsColumn(t, sqlDB, tbl, "default_root_folder_path") {
		t.Fatalf("%s.default_root_folder_path still present after down to 42 — down did not reverse", tbl)
	}

	if err := m.Migrate(43); err != nil {
		t.Fatalf("migrate up to 43 again: %v", err)
	}
	if !hasSettingsColumn(t, sqlDB, tbl, "default_quality_profile_id") {
		t.Fatalf("%s.default_quality_profile_id missing after re-up to 43", tbl)
	}
	if !hasSettingsColumn(t, sqlDB, tbl, "default_root_folder_path") {
		t.Fatalf("%s.default_root_folder_path missing after re-up to 43", tbl)
	}
}

// hasSettingsColumn reports whether `table` has a column named `col`, via
// SQLite's pragma_table_info. Table-parameterised sibling of the series-only
// hasColumn helper (migration_000034_roundtrip_test.go). The table name is an
// internal test constant, not user input.
func hasSettingsColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, table))
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
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return false
}
