package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000041_UpDownUp_SQLite drives golang-migrate to version 41 (up),
// steps back to 40 (down 000041), then forward to 41 again against the embedded
// SQLite migration set — proving 000041 down reverses the up cleanly (drops the
// series.tmdb_changed_at column + its partial index via the atlas-generated
// table rebuild, and drops the tmdb_changes_state table) and re-applies. Uses an
// in-memory SQLite DB.
func TestMigration000041_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(41); err != nil {
		t.Fatalf("migrate up to 41: %v", err)
	}
	if !hasColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("series.tmdb_changed_at missing after up to 41")
	}
	if !hasTable(t, sqlDB, "tmdb_changes_state") {
		t.Fatalf("tmdb_changes_state table missing after up to 41")
	}

	if err := m.Migrate(40); err != nil {
		t.Fatalf("migrate down to 40: %v", err)
	}
	if hasColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("series.tmdb_changed_at still present after down to 40 — down did not reverse")
	}
	if hasTable(t, sqlDB, "tmdb_changes_state") {
		t.Fatalf("tmdb_changes_state still present after down to 40 — down did not reverse")
	}

	if err := m.Migrate(41); err != nil {
		t.Fatalf("migrate up to 41 again: %v", err)
	}
	if !hasColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("series.tmdb_changed_at missing after re-up to 41")
	}
	if !hasTable(t, sqlDB, "tmdb_changes_state") {
		t.Fatalf("tmdb_changes_state missing after re-up to 41")
	}
}
