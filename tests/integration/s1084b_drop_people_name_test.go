//go:build integration

// S-1084b closure test — verifies migration 000037 drops people.name on
// both dialects, that original_name plus the other 13 columns survive,
// that both indexes (people_tmdb_id, people_imdb_id) and the inbound FKs
// (person_credits, person_biographies, people_texts) survive the SQLite
// table rebuild, and that the down migration re-adds name NULLABLE.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestS1084bDropPeopleName_ColumnsGone — after migrating to version 37,
// people has no `name` column, still has original_name + all other
// non-i18n columns, and both indexes survive.
func TestS1084bDropPeopleName_ColumnsGone(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(37), "Migrate(37) must succeed on %s", b.name)

			cols := e3bColumnSet(t, ctx, db, b.name, "people")
			assert.NotContains(t, cols, "name", "people.name must be dropped")
			for _, c := range []string{
				"id", "tmdb_id", "imdb_id", "hydration", "original_name",
				"gender", "birthday", "deathday", "place_of_birth",
				"known_for_department", "popularity", "profile_asset",
				"enrichment_synced_at", "created_at", "updated_at",
			} {
				assert.Contains(t, cols, c, "people.%s must survive the drop", c)
			}
			assert.Len(t, cols, 15, "people must have exactly 15 columns post-drop on %s", b.name)

			assert.True(t, s1084bIndexExists(t, ctx, db, b.name, "people_tmdb_id"),
				"people_tmdb_id index must survive on %s", b.name)
			assert.True(t, s1084bIndexExists(t, ctx, db, b.name, "people_imdb_id"),
				"people_imdb_id index must survive on %s", b.name)
		})
	}
}

// TestS1084bDropPeopleName_InboundFKsSurvive — the SQLite up migration
// rebuilds `people` via CREATE new_people + copy + DROP + RENAME; this
// proves the tables holding a real FK to people(id) — person_credits,
// person_biographies, people_texts — still reference the rebuilt table
// and a live insert against each succeeds post-migration, and that the
// NO ACTION FK on person_credits still blocks a delete of a referenced
// person row.
func TestS1084bDropPeopleName_InboundFKsSurvive(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(37), "Migrate(37) must succeed on %s", b.name)

			var personID int64
			require.NoError(t, db.QueryRowContext(ctx,
				fmt.Sprintf(`INSERT INTO people (hydration, original_name) VALUES ('stub', %s) RETURNING id`,
					s1084bPlaceholder(b.name, 1)),
				"Original Name",
			).Scan(&personID), "seed a post-drop people row on %s", b.name)

			_, err := db.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO people_texts (person_id, language, name, updated_at)
				 VALUES (%s, 'en-US', %s, CURRENT_TIMESTAMP)`,
					s1084bPlaceholder(b.name, 1), s1084bPlaceholder(b.name, 2)),
				personID, "Localized Name")
			require.NoError(t, err, "insert people_texts referencing the rebuilt people table on %s", b.name)

			_, err = db.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO person_biographies (person_id, language, biography, updated_at)
				 VALUES (%s, 'en-US', %s, CURRENT_TIMESTAMP)`,
					s1084bPlaceholder(b.name, 1), s1084bPlaceholder(b.name, 2)),
				personID, "A biography.")
			require.NoError(t, err, "insert person_biographies referencing the rebuilt people table on %s", b.name)

			_, err = db.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO person_credits (person_id, tmdb_credit_id, media_type, tmdb_media_id, title, kind, updated_at, created_at)
				 VALUES (%s, %s, 'tv', 999, %s, 'cast', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
					s1084bPlaceholder(b.name, 1), s1084bPlaceholder(b.name, 2), s1084bPlaceholder(b.name, 3)),
				personID, "cast:1", "Some Series")
			require.NoError(t, err, "insert person_credits referencing the rebuilt people table on %s", b.name)

			// Deleting the person row must be blocked by the surviving
			// NO ACTION FK on person_credits — proves the constraint
			// itself (not just the rows) survived the SQLite rebuild.
			_, err = db.ExecContext(ctx,
				fmt.Sprintf(`DELETE FROM people WHERE id = %s`, s1084bPlaceholder(b.name, 1)),
				personID)
			assert.Error(t, err, "deleting a person with a live person_credits row must be blocked by the surviving FK on %s", b.name)
		})
	}
}

// TestS1084bDropPeopleName_DownReAddsNullableOnSeededTable — rolling back
// one step (37 → 36) on a table pre-seeded with a post-drop row succeeds,
// proving name re-adds NULLABLE (a NOT NULL re-add would fail).
func TestS1084bDropPeopleName_DownReAddsNullableOnSeededTable(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Migrate(37), "Migrate(37) must succeed on %s", b.name)

			_, err := db.ExecContext(ctx,
				fmt.Sprintf(`INSERT INTO people (hydration, original_name) VALUES ('stub', %s)`,
					s1084bPlaceholder(b.name, 1)),
				"Seeded Person")
			require.NoError(t, err, "seed a post-drop people row on %s", b.name)

			require.NoError(t, m.Steps(-1), "down 37→36 must succeed on a populated table (%s)", b.name)

			cols := e3bColumnSet(t, ctx, db, b.name, "people")
			assert.Contains(t, cols, "name", "down re-adds people.name")

			// The re-added column must be NULLABLE — a NOT NULL re-add
			// would already have failed the seeded row above at
			// Steps(-1) time, but assert the live nullability directly
			// too as a second, independent signal.
			assert.True(t, s1084bColumnNullable(t, ctx, db, b.name, "people", "name"),
				"people.name must be re-added NULLABLE on %s", b.name)
		})
	}
}

func s1084bPlaceholder(driver string, n int) string {
	if driver == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

func s1084bIndexExists(t *testing.T, ctx context.Context, db *sql.DB, dialect, idxName string) bool {
	t.Helper()
	var q string
	switch dialect {
	case "postgres":
		q = `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`
	case "sqlite":
		q = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'index' AND name = ?)`
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	var exists bool
	require.NoError(t, db.QueryRowContext(ctx, q, idxName).Scan(&exists))
	return exists
}

func s1084bColumnNullable(t *testing.T, ctx context.Context, db *sql.DB, dialect, tbl, col string) bool {
	t.Helper()
	switch dialect {
	case "postgres":
		var nullable string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT is_nullable FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
			tbl, col).Scan(&nullable))
		return nullable == "YES"
	case "sqlite":
		rows, err := db.QueryContext(ctx, `SELECT name, "notnull" FROM pragma_table_info(?)`, tbl)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name string
			var notNull int
			require.NoError(t, rows.Scan(&name, &notNull))
			if name == col {
				return notNull == 0
			}
		}
		require.NoError(t, rows.Err())
		t.Fatalf("column %q not found on table %q", col, tbl)
		return false
	default:
		t.Fatalf("unknown dialect %q", dialect)
		return false
	}
}
