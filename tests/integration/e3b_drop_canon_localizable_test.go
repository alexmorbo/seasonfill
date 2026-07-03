//go:build integration

// S-E3b closure test — verifies migration 000028 drops the 6 localizable
// canon columns on both dialects and that the down re-adds them NULLABLE
// on a populated table (a NOT NULL re-add would fail rollback).
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e3bColumnSet returns the set of column names for tbl on the given
// dialect, via each backend's live introspection surface.
func e3bColumnSet(t *testing.T, ctx context.Context, db *sql.DB, dialect, tbl string) map[string]struct{} {
	t.Helper()
	var q string
	switch dialect {
	case "postgres":
		q = `SELECT column_name FROM information_schema.columns WHERE table_name = $1`
	case "sqlite":
		q = `SELECT name FROM pragma_table_info(?)`
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	rows, err := db.QueryContext(ctx, q, tbl)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		out[name] = struct{}{}
	}
	require.NoError(t, rows.Err())
	return out
}

// TestE3bDropCanonLocalizable_ColumnsGone — after migrating to version 28,
// the 6 localizable canon columns are absent on both dialects; original_title /
// original_language survive.
func TestE3bDropCanonLocalizable_ColumnsGone(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(28), "Migrate(28) must succeed on %s", b.name)

			seriesCols := e3bColumnSet(t, ctx, db, b.name, "series")
			for _, c := range []string{"title", "poster_asset", "backdrop_asset"} {
				assert.NotContains(t, seriesCols, c, "series.%s must be dropped", c)
			}
			assert.Contains(t, seriesCols, "original_title", "series.original_title must survive")
			assert.Contains(t, seriesCols, "original_language", "series.original_language must survive")

			seasonCols := e3bColumnSet(t, ctx, db, b.name, "seasons")
			for _, c := range []string{"name", "overview", "poster_asset"} {
				assert.NotContains(t, seasonCols, c, "seasons.%s must be dropped", c)
			}
			assert.Contains(t, seasonCols, "tmdb_season_id", "seasons.tmdb_season_id must survive")
		})
	}
}

// TestE3bDropCanonLocalizable_DownReAddsNullableOnSeededTable — rolling
// back one step (28 → 27) on a table pre-seeded with a row succeeds,
// proving title re-adds NULLABLE (a NOT NULL re-add would fail).
func TestE3bDropCanonLocalizable_DownReAddsNullableOnSeededTable(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(28), "Migrate(28) must succeed on %s", b.name)

			// Seed one series row (post-drop schema — no title column; every
			// NOT NULL column below either is provided or carries a DEFAULT).
			_, err := db.ExecContext(ctx,
				`INSERT INTO series (hydration, in_production, origin_countries) VALUES ('stub', false, '[]')`)
			require.NoError(t, err, "seed a post-drop series row on %s", b.name)

			// Roll back one step (28 → 27): re-adds the 6 columns NULLABLE.
			require.NoError(t, m.Steps(-1), "down 28→27 must succeed on a populated table (%s)", b.name)

			cols := e3bColumnSet(t, ctx, db, b.name, "series")
			assert.Contains(t, cols, "title", "down re-adds series.title")
			assert.Contains(t, cols, "poster_asset", "down re-adds series.poster_asset")
		})
	}
}
