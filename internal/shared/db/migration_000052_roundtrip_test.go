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

// TestMigration000052_UpDownUp_SQLite drives golang-migrate to 52
// (sonarr_instance -> arr_instance + type col + radarr_instance_settings),
// steps back to 51 (restores sonarr_instance, drops radarr settings), then
// forward to 52 again — proving 000052 down reverses up cleanly across the
// cyclic-FK table rebuild. It seeds a parent row plus a FK-dependent child
// before stepping down so the assertions prove real data survives the
// rebuild (not just table presence/absence): the arr_instance row keeps its
// backfilled type='sonarr', the child row survives with its FK intact, and
// the type column disappears on the way down. In-memory SQLite.
func TestMigration000052_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(52); err != nil {
		t.Fatalf("migrate up to 52: %v", err)
	}
	if !hasTable(t, sqlDB, "arr_instance") {
		t.Fatal("arr_instance missing after up to 52")
	}
	if hasTable(t, sqlDB, "sonarr_instance") {
		t.Fatal("sonarr_instance should be gone after up to 52")
	}
	if !hasTable(t, sqlDB, "radarr_instance_settings") {
		t.Fatal("radarr_instance_settings missing after up to 52")
	}
	if !hasTable(t, sqlDB, "sonarr_instance_settings") {
		t.Fatal("sonarr_instance_settings must survive the rename")
	}

	// Seed a parent row (type omitted -> DEFAULT 'sonarr' backfills it) plus a
	// FK-dependent child so the down/up rebuild is exercised on real data.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO arr_instance (name, url) VALUES ('inst1', 'http://sonarr.local')`,
	); err != nil {
		t.Fatalf("seed arr_instance: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO instance_secret (instance_name, secret_name, encrypted_value)
		 VALUES ('inst1', 'apiKey', x'0102')`,
	); err != nil {
		t.Fatalf("seed instance_secret: %v", err)
	}
	if got := columnValue(t, sqlDB, `SELECT type FROM arr_instance WHERE name = 'inst1'`); got != "sonarr" {
		t.Fatalf("type should backfill to 'sonarr' after up, got %q", got)
	}

	if err := m.Migrate(51); err != nil {
		t.Fatalf("migrate down to 51: %v", err)
	}
	if !hasTable(t, sqlDB, "sonarr_instance") {
		t.Fatal("sonarr_instance missing after down to 51")
	}
	if hasTable(t, sqlDB, "arr_instance") {
		t.Fatal("arr_instance should be gone after down to 51")
	}
	if hasTable(t, sqlDB, "radarr_instance_settings") {
		t.Fatal("radarr_instance_settings should be dropped after down to 51")
	}
	if n := rowCount(t, sqlDB, `SELECT COUNT(*) FROM sonarr_instance WHERE name = 'inst1'`); n != 1 {
		t.Fatalf("parent row must survive down rebuild, got %d rows", n)
	}
	if n := rowCount(t, sqlDB, `SELECT COUNT(*) FROM instance_secret WHERE instance_name = 'inst1'`); n != 1 {
		t.Fatalf("child row must survive down rebuild with FK intact, got %d rows", n)
	}
	if tableHasColumn(t, sqlDB, "sonarr_instance", "type") {
		t.Fatal("type column should be gone after down to 51")
	}
	assertNoFKViolations(t, sqlDB, "after down to 51")

	if err := m.Migrate(52); err != nil {
		t.Fatalf("migrate up to 52 again: %v", err)
	}
	if !hasTable(t, sqlDB, "arr_instance") {
		t.Fatal("arr_instance missing after re-up to 52")
	}
	if n := rowCount(t, sqlDB, `SELECT COUNT(*) FROM arr_instance WHERE name = 'inst1'`); n != 1 {
		t.Fatalf("parent row must survive re-up rebuild, got %d rows", n)
	}
	if n := rowCount(t, sqlDB, `SELECT COUNT(*) FROM instance_secret WHERE instance_name = 'inst1'`); n != 1 {
		t.Fatalf("child row must survive re-up rebuild with FK intact, got %d rows", n)
	}
	if got := columnValue(t, sqlDB, `SELECT type FROM arr_instance WHERE name = 'inst1'`); got != "sonarr" {
		t.Fatalf("type should be 'sonarr' after re-up, got %q", got)
	}
	assertNoFKViolations(t, sqlDB, "after re-up to 52")
}

func rowCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func columnValue(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(context.Background(), query).Scan(&v); err != nil {
		t.Fatalf("value query %q: %v", query, err)
	}
	return v
}

func tableHasColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		if name == col {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	return false
}

// assertNoFKViolations runs PRAGMA foreign_key_check, which returns one row per
// dangling reference; a clean rebuild leaves zero.
func assertNoFKViolations(t *testing.T, db *sql.DB, when string) {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check %s: %v", when, err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatalf("foreign key violation detected %s", when)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err %s: %v", when, err)
	}
}
