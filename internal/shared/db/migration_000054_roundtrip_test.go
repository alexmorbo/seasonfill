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

// TestMigration000054_UpDownUp_SQLite drives golang-migrate to 54 (movie
// refresh pipeline: movies.tmdb_changed_at column + movie_changes_state
// cursor table), steps back to 53 (drops both), then forward to 54 again —
// proving 000054 down reverses up cleanly. In-memory SQLite.
func TestMigration000054_UpDownUp_SQLite(t *testing.T) {
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

	if err := m.Migrate(54); err != nil {
		t.Fatalf("migrate up to 54: %v", err)
	}
	if !hasTable(t, sqlDB, "movie_changes_state") {
		t.Fatalf("movie_changes_state missing after up to 54")
	}
	if !hasMoviesColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("movies.tmdb_changed_at missing after up to 54")
	}

	// The single-row (id=1) cursor + the changed-mark column round-trip on
	// real data so the down/up cycle is exercised on live rows.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movies (id, tmdb_id, title, hydration) VALUES (1, 42, 'Dune', 'full')`,
	); err != nil {
		t.Fatalf("seed movies: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`UPDATE movies SET tmdb_changed_at = CURRENT_TIMESTAMP WHERE id = 1`,
	); err != nil {
		t.Fatalf("stamp tmdb_changed_at: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movie_changes_state (id, schema_version) VALUES (1, 1)`,
	); err != nil {
		t.Fatalf("seed movie_changes_state: %v", err)
	}
	// CHECK (id = 1) must reject any other id.
	if _, err := sqlDB.ExecContext(context.Background(),
		`INSERT INTO movie_changes_state (id, schema_version) VALUES (2, 1)`,
	); err == nil {
		t.Fatalf("movie_changes_state accepted id=2, want CHECK(id=1) rejection")
	}
	assertNoFKViolations(t, sqlDB, "after seed at 54")

	if err := m.Migrate(53); err != nil {
		t.Fatalf("migrate down to 53: %v", err)
	}
	if hasTable(t, sqlDB, "movie_changes_state") {
		t.Fatalf("movie_changes_state should be gone after down to 53")
	}
	if hasMoviesColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("movies.tmdb_changed_at should be gone after down to 53")
	}

	if err := m.Migrate(54); err != nil {
		t.Fatalf("migrate up to 54 again: %v", err)
	}
	if !hasTable(t, sqlDB, "movie_changes_state") {
		t.Fatalf("movie_changes_state missing after re-up to 54")
	}
	if !hasMoviesColumn(t, sqlDB, "tmdb_changed_at") {
		t.Fatalf("movies.tmdb_changed_at missing after re-up to 54")
	}
}

// hasMoviesColumn reports whether the movies table carries the named column
// (sibling of the series-scoped hasColumn helper).
func hasMoviesColumn(t *testing.T, db *sql.DB, col string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info('movies')`)
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
