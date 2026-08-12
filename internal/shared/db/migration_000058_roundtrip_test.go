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

// TestMigration000058_UpDownUp_SQLite drives golang-migrate to 58 (per-user
// user_id retrofit on discovery_blocklist / followed_series /
// notification_agents), steps back to 57, then forward to 58 again.
func TestMigration000058_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(58); err != nil {
		t.Fatalf("migrate up to 58: %v", err)
	}
	for _, tc := range []struct{ table, col string }{
		{"discovery_blocklist", "user_id"},
		{"followed_series", "user_id"},
		{"notification_agents", "user_id"},
	} {
		if !hasTableColumn(t, sqlDB, tc.table, tc.col) {
			t.Fatalf("%s.%s missing after up to 58", tc.table, tc.col)
		}
	}
	assertBlocklistPerUserUnique(t, sqlDB)
	assertFollowedCompositePK(t, sqlDB)

	// The per-user assertions above intentionally create rows that share a
	// series_id / (kind, ref_id) across two users. The 058 down migration
	// restores the single-column PK / global UNIQUE, which is lossy on such
	// rows by design (documented in 000058_per_user_retrofit.down.sql), so
	// clear the per-user tables before stepping back to 57.
	for _, tbl := range []string{"followed_series", "discovery_blocklist"} {
		if _, err := sqlDB.ExecContext(context.Background(), "DELETE FROM "+tbl); err != nil {
			t.Fatalf("cleanup %s before down: %v", tbl, err)
		}
	}

	if err := m.Migrate(57); err != nil {
		t.Fatalf("migrate down to 57: %v", err)
	}
	if hasTableColumn(t, sqlDB, "followed_series", "user_id") {
		t.Fatalf("followed_series.user_id should be gone after down to 57")
	}
	if err := m.Migrate(58); err != nil {
		t.Fatalf("migrate up to 58 again: %v", err)
	}
	if !hasTableColumn(t, sqlDB, "notification_agents", "user_id") {
		t.Fatalf("notification_agents.user_id missing after re-up to 58")
	}
}

// hasTableColumn reports whether table has a column named col (PRAGMA
// table_info). Generalizes the series-only hasColumn helper.
func hasTableColumn(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
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

// assertBlocklistPerUserUnique proves the same (kind, ref_id) is allowed for
// two different users but rejected within one user.
func assertBlocklistPerUserUnique(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, fatal string) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", fatal, err)
		}
	}
	exec(`INSERT INTO users (id, username, role, updated_at) VALUES (1, 'a', 'admin', CURRENT_TIMESTAMP)`, "seed user 1")
	exec(`INSERT INTO users (id, username, role, updated_at) VALUES (2, 'b', 'user', CURRENT_TIMESTAMP)`, "seed user 2")
	exec(`INSERT INTO discovery_blocklist (user_id, kind, ref_id) VALUES (1, 'tmdb', 42)`, "u1 blocks 42")
	exec(`INSERT INTO discovery_blocklist (user_id, kind, ref_id) VALUES (2, 'tmdb', 42)`, "u2 blocks 42 (per-user OK)")
	if _, err := db.ExecContext(ctx, `INSERT INTO discovery_blocklist (user_id, kind, ref_id) VALUES (1, 'tmdb', 42)`); err == nil {
		t.Fatalf("duplicate (user_id, kind, ref_id) must fail")
	}
}

// assertFollowedCompositePK proves the same series is followable by two users
// but not twice by one.
func assertFollowedCompositePK(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(q, fatal string) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", fatal, err)
		}
	}
	// series row for FK target (columns beyond id/tmdb_id carry DB defaults).
	exec(`INSERT INTO series (id, tmdb_id, original_title) VALUES (7, 700, 'x')`, "seed series")
	exec(`INSERT INTO followed_series (user_id, series_id) VALUES (1, 7)`, "u1 follows 7")
	exec(`INSERT INTO followed_series (user_id, series_id) VALUES (2, 7)`, "u2 follows 7 (per-user OK)")
	if _, err := db.ExecContext(ctx, `INSERT INTO followed_series (user_id, series_id) VALUES (1, 7)`); err == nil {
		t.Fatalf("duplicate (user_id, series_id) must fail")
	}
}
