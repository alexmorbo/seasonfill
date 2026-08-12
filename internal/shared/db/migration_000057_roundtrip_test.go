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

// TestMigration000057_UpDownUp_SQLite drives golang-migrate to 57 (Jellyfin
// user id column + partial-unique index), steps back to 56, then forward to 57
// again — proving 000057 down reverses up cleanly. In-memory SQLite.
func TestMigration000057_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(57); err != nil {
		t.Fatalf("migrate up to 57: %v", err)
	}
	if !hasUsersColumn(t, sqlDB, "jellyfin_user_id") {
		t.Fatalf("users.jellyfin_user_id missing after up to 57")
	}
	assertJellyfinPartialUnique(t, sqlDB)

	if err := m.Migrate(56); err != nil {
		t.Fatalf("migrate down to 56: %v", err)
	}
	if hasUsersColumn(t, sqlDB, "jellyfin_user_id") {
		t.Fatalf("users.jellyfin_user_id should be gone after down to 56")
	}
	if err := m.Migrate(57); err != nil {
		t.Fatalf("migrate up to 57 again: %v", err)
	}
	if !hasUsersColumn(t, sqlDB, "jellyfin_user_id") {
		t.Fatalf("users.jellyfin_user_id missing after re-up to 57")
	}
}

// assertJellyfinPartialUnique proves the partial-unique index allows many NULL
// jellyfin_user_id rows but rejects a duplicate non-NULL value.
func assertJellyfinPartialUnique(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, fatal string) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", fatal, err)
		}
	}
	exec(`INSERT INTO users (id, username, updated_at) VALUES (1, 'a', CURRENT_TIMESTAMP)`, "seed a")
	exec(`INSERT INTO users (id, username, updated_at) VALUES (2, 'b', CURRENT_TIMESTAMP)`, "seed b (NULL jellyfin_user_id #2)")
	exec(`INSERT INTO users (id, username, jellyfin_user_id, updated_at) VALUES (3, 'c', 'jf-1', CURRENT_TIMESTAMP)`, "first non-NULL")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, jellyfin_user_id, updated_at) VALUES (4, 'd', 'jf-1', CURRENT_TIMESTAMP)`,
	); err == nil {
		t.Fatalf("duplicate non-NULL jellyfin_user_id must fail (partial UNIQUE)")
	}
}
