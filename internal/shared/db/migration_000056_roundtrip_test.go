package database

import (
	"context"
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000056_UpDownUp_SQLite drives golang-migrate to 56 (requests
// table + admin-perms backfill), steps back to 55 (drops requests), then
// forward to 56 again — proving 000056 down reverses up cleanly.
func TestMigration000056_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(56); err != nil {
		t.Fatalf("migrate up to 56: %v", err)
	}
	if !hasTable(t, sqlDB, "requests") {
		t.Fatalf("requests missing after up to 56")
	}

	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO users (id, username, role, updated_at) VALUES (1, 'admin', 'admin', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	// Insert a pending tv request.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO requests (user_id, media_type, tmdb_id, payload, status, updated_at)
		 VALUES (1, 'tv', 1399, '{"media_type":"tv","external_id":1399}', 'pending', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed request: %v", err)
	}
	// pending partial-unique: a second identical pending insert must fail.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO requests (user_id, media_type, tmdb_id, payload, status, updated_at)
		 VALUES (1, 'tv', 1399, '{}', 'pending', CURRENT_TIMESTAMP)`,
	); err == nil {
		t.Fatalf("expected requests_pending_uniq to reject duplicate pending row")
	}
	// FK backstop: orphan user_id flagged by foreign_key_check.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO requests (user_id, media_type, tmdb_id, payload, status, updated_at)
		 VALUES (9999, 'tv', 42, '{}', 'pending', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed orphan request: %v", err)
	}
	if !hasFKViolation(t, sqlDB) {
		t.Fatalf("foreign_key_check did not flag orphan user_id=9999 — FK missing on requests")
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`DELETE FROM requests WHERE user_id = 9999`,
	); err != nil {
		t.Fatalf("cleanup orphan: %v", err)
	}

	if err := m.Migrate(55); err != nil {
		t.Fatalf("migrate down to 55: %v", err)
	}
	if hasTable(t, sqlDB, "requests") {
		t.Fatalf("requests should be gone after down to 55")
	}
	if err := m.Migrate(56); err != nil {
		t.Fatalf("migrate up to 56 again: %v", err)
	}
	if !hasTable(t, sqlDB, "requests") {
		t.Fatalf("requests missing after re-up to 56")
	}
}

// TestMigration000056_AdminBackfill_SQLite proves the highest-risk statement:
// seed an admin with perms=0 at 55, run 56, assert the backfill flipped every
// perm column to 1 for role='admin'.
func TestMigration000056_AdminBackfill_SQLite(t *testing.T) {
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
	// Pre-RBAC admin: perms default false (0).
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO users (id, username, role, auto_approve, request, manage_requests, manage_users, request_4k, updated_at)
		 VALUES (1, 'admin', 'admin', 0, 0, 0, 0, 0, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed admin at 55: %v", err)
	}
	// A plain user must NOT be touched by the backfill.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO users (id, username, role, auto_approve, request, manage_requests, manage_users, request_4k, updated_at)
		 VALUES (2, 'plain', 'user', 0, 0, 0, 0, 0, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed user at 55: %v", err)
	}

	if err := m.Migrate(56); err != nil {
		t.Fatalf("migrate up to 56: %v", err)
	}

	var aa, req, mr, mu, r4k int
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT auto_approve, request, manage_requests, manage_users, request_4k FROM users WHERE role='admin'`,
	).Scan(&aa, &req, &mr, &mu, &r4k); err != nil {
		t.Fatalf("read admin perms: %v", err)
	}
	if aa != 1 || req != 1 || mr != 1 || mu != 1 || r4k != 1 {
		t.Fatalf("admin perms not backfilled: auto_approve=%d request=%d manage_requests=%d manage_users=%d request_4k=%d", aa, req, mr, mu, r4k)
	}

	var userAA int
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT auto_approve FROM users WHERE role='user'`,
	).Scan(&userAA); err != nil {
		t.Fatalf("read user perms: %v", err)
	}
	if userAA != 0 {
		t.Fatalf("plain user auto_approve = %d, want 0 (backfill must not touch role='user')", userAA)
	}
}
