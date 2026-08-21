package database

import (
	"io/fs"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/alexmorbo/seasonfill/infrastructure/database/migrations"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// TestMigration000065_UpDownUp_SQLite drives golang-migrate to 65
// (torrent_movie_map, the ADR-0023 B1.1 movie bridge), steps back to 64
// (drops it), then forward to 65 again — proving 000065 down reverses up
// cleanly. Also asserts torrent_series_map survives every step untouched:
// the movie table is strictly additive.
func TestMigration000065_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(65); err != nil {
		t.Fatalf("migrate up to 65: %v", err)
	}
	if !hasTable(t, sqlDB, "torrent_movie_map") {
		t.Fatal("torrent_movie_map missing after up to 65")
	}
	for _, c := range []string{
		"instance_name", "torrent_hash", "radarr_movie_id",
		"source", "provenance", "created_at",
	} {
		if !hasTableColumn(t, sqlDB, "torrent_movie_map", c) {
			t.Fatalf("torrent_movie_map.%s missing after up to 65", c)
		}
	}
	// Movies have no seasons — the series column must NOT be mirrored.
	if hasTableColumn(t, sqlDB, "torrent_movie_map", "season_number") {
		t.Fatal("torrent_movie_map.season_number must not exist — movies have no seasons")
	}
	if !hasTable(t, sqlDB, "torrent_series_map") {
		t.Fatal("torrent_series_map must be untouched by 000065")
	}

	if err := m.Migrate(64); err != nil {
		t.Fatalf("migrate down to 64: %v", err)
	}
	if hasTable(t, sqlDB, "torrent_movie_map") {
		t.Fatal("torrent_movie_map should be gone after down to 64")
	}
	if !hasTable(t, sqlDB, "torrent_series_map") {
		t.Fatal("torrent_series_map must survive the 000065 down step")
	}

	if err := m.Migrate(65); err != nil {
		t.Fatalf("migrate up to 65 again: %v", err)
	}
	if !hasTable(t, sqlDB, "torrent_movie_map") {
		t.Fatal("torrent_movie_map missing after re-up to 65")
	}
}
