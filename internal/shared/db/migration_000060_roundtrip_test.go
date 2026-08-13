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

// TestMigration000060_UpDownUp_SQLite drives golang-migrate to 60 (movie
// vertical schema: movie_genres, movie_companies, movie_keywords,
// movie_recommendations, movie_videos), exercises FK CASCADE + the
// self-reference CHECK on live rows, steps back to 59 (drops all 5), then
// forward to 60 again — proving 000060 down reverses up cleanly. In-memory
// SQLite.
func TestMigration000060_UpDownUp_SQLite(t *testing.T) {
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

	newTables := []string{
		"movie_genres", "movie_companies", "movie_keywords",
		"movie_recommendations", "movie_videos",
	}

	if err := m.Migrate(60); err != nil {
		t.Fatalf("migrate up to 60: %v", err)
	}
	for _, tbl := range newTables {
		if !hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s missing after up to 60", tbl)
		}
	}

	ctx := context.Background()
	exec := func(q, fatal string) {
		if _, err := sqlDB.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", fatal, err)
		}
	}
	// Seed dimensions + two movies to exercise FKs on live data.
	exec(`INSERT INTO movies (id, tmdb_id, title, hydration) VALUES (1, 42, 'Dune', 'full')`, "seed movie 1")
	exec(`INSERT INTO movies (id, tmdb_id, title, hydration) VALUES (2, 43, 'Dune II', 'full')`, "seed movie 2")
	exec(`INSERT INTO genres (id, tmdb_id) VALUES (100, 878)`, "seed genre")
	exec(`INSERT INTO keywords (id, tmdb_id) VALUES (200, 4565)`, "seed keyword")
	exec(`INSERT INTO production_companies (id, tmdb_id, name) VALUES (300, 923, 'Legendary')`, "seed company")
	exec(`INSERT INTO movie_genres (movie_id, genre_id, position) VALUES (1, 100, 0)`, "movie_genres row")
	exec(`INSERT INTO movie_keywords (movie_id, keyword_id) VALUES (1, 200)`, "movie_keywords row")
	exec(`INSERT INTO movie_companies (movie_id, company_id, position) VALUES (1, 300, 0)`, "movie_companies row")
	exec(`INSERT INTO movie_recommendations (movie_id, recommended_movie_id, position) VALUES (1, 2, 0)`, "movie_recommendations row")
	exec(`INSERT INTO movie_videos (movie_id, tmdb_video_id, name, official) VALUES (1, 'vid-1', 'Trailer', 1)`, "movie_videos row")

	// self-reference CHECK must reject movie_id == recommended_movie_id.
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO movie_recommendations (movie_id, recommended_movie_id, position) VALUES (1, 1, 1)`,
	); err == nil {
		t.Fatal("movie_recommendations accepted self-reference; want CHECK rejection")
	}
	// partial-unique tmdb_video_id must reject a duplicate non-NULL id.
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO movie_videos (movie_id, tmdb_video_id, name, official) VALUES (2, 'vid-1', 'Dup', 0)`,
	); err == nil {
		t.Fatal("movie_videos accepted duplicate tmdb_video_id; want UNIQUE rejection")
	}
	assertNoFKViolations(t, sqlDB, "after seed at 60")

	if err := m.Migrate(59); err != nil {
		t.Fatalf("migrate down to 59: %v", err)
	}
	for _, tbl := range newTables {
		if hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s should be gone after down to 59", tbl)
		}
	}

	if err := m.Migrate(60); err != nil {
		t.Fatalf("migrate up to 60 again: %v", err)
	}
	for _, tbl := range newTables {
		if !hasTable(t, sqlDB, tbl) {
			t.Fatalf("%s missing after re-up to 60", tbl)
		}
	}
}
