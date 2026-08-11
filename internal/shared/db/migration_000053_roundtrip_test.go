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

// TestMigration000053_UpDownUp_SQLite drives golang-migrate to 53 (movie
// canon: movies + movie_i18n + movie_states + collections), steps back to 52
// (drops all four), then forward to 53 again — proving 000053 down reverses
// up cleanly. Simpler than 000052 (pure additive CREATE TABLE, no rebuild),
// so the assertions focus on table presence/absence plus FK integrity: a
// seeded movies row + dependent movie_i18n/movie_states rows survive up, and
// PRAGMA foreign_key_check stays clean. In-memory SQLite.
func TestMigration000053_UpDownUp_SQLite(t *testing.T) {
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

	movieTables := []string{"movies", "movie_i18n", "movie_states", "collections"}

	if err := m.Migrate(53); err != nil {
		t.Fatalf("migrate up to 53: %v", err)
	}
	for _, tbl := range movieTables {
		if !hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s missing after up to 53", tbl)
		}
	}

	// Seed canon + dependent rows so the down/up cycle is exercised on real
	// FK-linked data (movie_i18n + movie_states both FK → movies.id).
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movies (id, tmdb_id, title, hydration) VALUES (1, 42, 'Dune', 'full')`,
	); err != nil {
		t.Fatalf("seed movies: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movie_i18n (movie_id, language, title) VALUES (1, 'ru-RU', 'Дюна')`,
	); err != nil {
		t.Fatalf("seed movie_i18n: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movie_states (instance_name, radarr_movie_id, movie_id, title_slug, updated_at)
		 VALUES ('main', 7, 1, 'dune', CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("seed movie_states: %v", err)
	}
	assertNoFKViolations(t, sqlDB, "after seed at 53")

	if err := m.Migrate(52); err != nil {
		t.Fatalf("migrate down to 52: %v", err)
	}
	for _, tbl := range movieTables {
		if hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s should be gone after down to 52", tbl)
		}
	}
	assertNoFKViolations(t, sqlDB, "after down to 52")

	if err := m.Migrate(53); err != nil {
		t.Fatalf("migrate up to 53 again: %v", err)
	}
	for _, tbl := range movieTables {
		if !hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s missing after re-up to 53", tbl)
		}
	}
	assertNoFKViolations(t, sqlDB, "after re-up to 53")
}
