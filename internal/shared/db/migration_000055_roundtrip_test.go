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

// TestMigration000055_UpDownUp_SQLite drives golang-migrate to 55 (RBAC:
// 5 permission bool columns on users + user_instance_access ACL table),
// steps back to 54 (drops the table + the 5 columns), then forward to 55
// again — proving 000055 down reverses up cleanly. In-memory SQLite.
func TestMigration000055_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(55); err != nil {
		t.Fatalf("migrate up to 55: %v", err)
	}
	if !hasTable(t, sqlDB, "user_instance_access") {
		t.Fatalf("user_instance_access missing after up to 55")
	}
	for _, col := range []string{"auto_approve", "request", "manage_requests", "manage_users", "request_4k"} {
		if !hasUsersColumn(t, sqlDB, col) {
			t.Fatalf("users.%s missing after up to 55", col)
		}
	}

	// Seed a user + an access row so the down/up cycle runs on live rows.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO users (id, username, updated_at) VALUES (1, 'admin', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	// can_request DEFAULT true when omitted.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO user_instance_access (user_id, instance_name) VALUES (1, 'main')`,
	); err != nil {
		t.Fatalf("seed user_instance_access: %v", err)
	}
	var canRequest bool
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT can_request FROM user_instance_access WHERE user_id = 1 AND instance_name = 'main'`,
	).Scan(&canRequest); err != nil {
		t.Fatalf("read can_request: %v", err)
	}
	if !canRequest {
		t.Fatalf("can_request = false, want DEFAULT true")
	}
	// FK → users CASCADE is defined on the migrated table. SQLite FK
	// enforcement is off on this connection pool (the dev DSN sets no
	// foreign_keys pragma), so prove the constraint exists the way the
	// sibling roundtrip tests do — via PRAGMA foreign_key_check, which
	// reports violating rows regardless of enforcement. Insert an orphan,
	// assert the check flags it (constraint present), then remove it so
	// the subsequent clean assertNoFKViolations passes.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO user_instance_access (user_id, instance_name) VALUES (9999, 'main')`,
	); err != nil {
		t.Fatalf("seed orphan user_instance_access: %v", err)
	}
	if !hasFKViolation(t, sqlDB) {
		t.Fatalf("foreign_key_check did not flag orphan user_id=9999 — FK constraint missing on user_instance_access")
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`DELETE FROM user_instance_access WHERE user_id = 9999`,
	); err != nil {
		t.Fatalf("cleanup orphan user_instance_access: %v", err)
	}
	assertNoFKViolations(t, sqlDB, "after seed at 55")

	if err := m.Migrate(54); err != nil {
		t.Fatalf("migrate down to 54: %v", err)
	}
	if hasTable(t, sqlDB, "user_instance_access") {
		t.Fatalf("user_instance_access should be gone after down to 54")
	}
	if hasUsersColumn(t, sqlDB, "auto_approve") {
		t.Fatalf("users.auto_approve should be gone after down to 54")
	}

	if err := m.Migrate(55); err != nil {
		t.Fatalf("migrate up to 55 again: %v", err)
	}
	if !hasTable(t, sqlDB, "user_instance_access") {
		t.Fatalf("user_instance_access missing after re-up to 55")
	}
	if !hasUsersColumn(t, sqlDB, "request_4k") {
		t.Fatalf("users.request_4k missing after re-up to 55")
	}
}

// hasFKViolation reports whether PRAGMA foreign_key_check finds any
// violating row. Unlike assertNoFKViolations (which fails on a hit), this
// returns the boolean so the caller can assert a violation is EXPECTED —
// used to prove a FK constraint is present on the migrated schema even
// when per-connection FK enforcement is off.
func hasFKViolation(t *testing.T, db *sql.DB) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check rows err: %v", err)
	}
	return found
}

// hasUsersColumn reports whether the users table carries the named column
// (sibling of hasMoviesColumn in migration_000054_roundtrip_test.go).
func hasUsersColumn(t *testing.T, db *sql.DB, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info('users')`)
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
