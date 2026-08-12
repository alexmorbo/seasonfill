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

// TestMigration000059_UpDownUp_SQLite drives golang-migrate to 59 (per-user
// dispatch: user_id on notification_outbox + notified_events, notified_events
// PK becomes (user_id, event_type, entity_key)), steps back to 58, then
// forward to 59 again.
func TestMigration000059_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(59); err != nil {
		t.Fatalf("migrate up to 59: %v", err)
	}
	for _, tc := range []struct{ table, col string }{
		{"notification_outbox", "user_id"},
		{"notified_events", "user_id"},
	} {
		if !hasTableColumn(t, sqlDB, tc.table, tc.col) {
			t.Fatalf("%s.%s missing after up to 59", tc.table, tc.col)
		}
	}
	assertNotifiedEventsPerUserPK(t, sqlDB)

	// The per-user assertion inserts (u1,e,k) and (u2,e,k). The 059 down
	// migration restores the (event_type, entity_key) PK, which is lossy on
	// such cross-user rows by design (documented in the down migration), so
	// clear the table before stepping back to 58.
	if _, err := sqlDB.ExecContext(context.Background(), "DELETE FROM notified_events"); err != nil {
		t.Fatalf("cleanup notified_events before down: %v", err)
	}

	if err := m.Migrate(58); err != nil {
		t.Fatalf("migrate down to 58: %v", err)
	}
	if hasTableColumn(t, sqlDB, "notified_events", "user_id") {
		t.Fatalf("notified_events.user_id should be gone after down to 58")
	}
	if hasTableColumn(t, sqlDB, "notification_outbox", "user_id") {
		t.Fatalf("notification_outbox.user_id should be gone after down to 58")
	}
	if err := m.Migrate(59); err != nil {
		t.Fatalf("migrate up to 59 again: %v", err)
	}
	if !hasTableColumn(t, sqlDB, "notification_outbox", "user_id") {
		t.Fatalf("notification_outbox.user_id missing after re-up to 59")
	}
}

// assertNotifiedEventsPerUserPK proves the same (event_type, entity_key) is
// allowed for two different users but rejected within one user.
func assertNotifiedEventsPerUserPK(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	exec := func(q, fatal string) {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", fatal, err)
		}
	}
	exec(`INSERT INTO users (id, username, role, updated_at) VALUES (1, 'a', 'admin', CURRENT_TIMESTAMP)`, "seed user 1")
	exec(`INSERT INTO users (id, username, role, updated_at) VALUES (2, 'b', 'user', CURRENT_TIMESTAMP)`, "seed user 2")
	exec(`INSERT INTO notified_events (user_id, event_type, entity_key) VALUES (1, 'season.premiere', '42:2')`, "u1 marker")
	exec(`INSERT INTO notified_events (user_id, event_type, entity_key) VALUES (2, 'season.premiere', '42:2')`, "u2 marker (per-user OK)")
	if _, err := db.ExecContext(ctx, `INSERT INTO notified_events (user_id, event_type, entity_key) VALUES (1, 'season.premiere', '42:2')`); err == nil {
		t.Fatalf("duplicate (user_id, event_type, entity_key) must fail")
	}
}
