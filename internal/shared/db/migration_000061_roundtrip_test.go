package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000061_UpDownUp_SQLite drives golang-migrate to 61 (movie section
// stamps: 5 nullable timestamp columns on movies), steps back to 60 (drops the 5
// columns), then forward to 61 again — proving 000061 down reverses up cleanly.
// In-memory SQLite.
func TestMigration000061_UpDownUp_SQLite(t *testing.T) {
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

	cols := []string{
		"enrichment_text_synced_at", "enrichment_cast_synced_at",
		"enrichment_recs_synced_at", "enrichment_media_synced_at",
		"enrichment_keywords_synced_at",
	}

	if err := m.Migrate(61); err != nil {
		t.Fatalf("migrate up to 61: %v", err)
	}
	for _, c := range cols {
		if !hasTableColumn(t, sqlDB, "movies", c) {
			t.Fatalf("movies.%s missing after up to 61", c)
		}
	}

	if err := m.Migrate(60); err != nil {
		t.Fatalf("migrate down to 60: %v", err)
	}
	for _, c := range cols {
		if hasTableColumn(t, sqlDB, "movies", c) {
			t.Fatalf("movies.%s should be gone after down to 60", c)
		}
	}

	if err := m.Migrate(61); err != nil {
		t.Fatalf("migrate up to 61 again: %v", err)
	}
	for _, c := range cols {
		if !hasTableColumn(t, sqlDB, "movies", c) {
			t.Fatalf("movies.%s missing after re-up to 61", c)
		}
	}
}
