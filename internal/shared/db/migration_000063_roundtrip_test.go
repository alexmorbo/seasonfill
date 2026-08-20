package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000063_UpDownUp_SQLite drives golang-migrate to 63 (followed_movies
// watchlist table), steps back to 62 (drops it), then forward to 63 again —
// proving 000063 down reverses up cleanly. Also asserts followed_series survives
// every step untouched: the movie table is strictly additive.
func TestMigration000063_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(63); err != nil {
		t.Fatalf("migrate up to 63: %v", err)
	}
	if !hasTable(t, sqlDB, "followed_movies") {
		t.Fatal("followed_movies missing after up to 63")
	}
	for _, c := range []string{"user_id", "movie_id", "created_at"} {
		if !hasTableColumn(t, sqlDB, "followed_movies", c) {
			t.Fatalf("followed_movies.%s missing after up to 63", c)
		}
	}
	if !hasTable(t, sqlDB, "followed_series") {
		t.Fatal("followed_series must be untouched by 000063")
	}

	if err := m.Migrate(62); err != nil {
		t.Fatalf("migrate down to 62: %v", err)
	}
	if hasTable(t, sqlDB, "followed_movies") {
		t.Fatal("followed_movies should be gone after down to 62")
	}
	if !hasTable(t, sqlDB, "followed_series") {
		t.Fatal("followed_series must survive the 000063 down step")
	}

	if err := m.Migrate(63); err != nil {
		t.Fatalf("migrate up to 63 again: %v", err)
	}
	if !hasTable(t, sqlDB, "followed_movies") {
		t.Fatal("followed_movies missing after re-up to 63")
	}
}
