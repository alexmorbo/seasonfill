//go:build integration

// D-1-8 (story 461) — final D-1 acceptance gate. Verifies the 11 PRD §D-1
// testable acceptance bullets (bullet #12 SeriesWorker COALESCE is deferred
// to D-2 per PRD) against the 13-migration tree at
// infrastructure/database/migrations/{postgres,sqlite}.
//
// Bullet #10 (atlas migrate diff detects an index drop) lives in
// d1_acceptance_diff_test.go because it shells out to the atlas binary and
// is skipped when atlas is unavailable.
//
// All tests run on both backends — sqlite always, postgres opt-in via
// SEASONFILL_TEST_POSTGRES_ENABLE (mirrors the rest of the D-1 suite).
package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/database/schema"
)

// TestD1_Acceptance_SchemaSourceOfTruth covers PRD §D-1 bullet #1 —
// schema/schema.go describes the full target §4 in BOTH dialects. The
// canonical table set is pinned in d1AcceptanceTablesPostgres; any drift
// (an addCoreSeries-style append that forgets to update the list, or a
// removed table that nobody notices) trips this test before the inventory
// test even runs.
func TestD1_Acceptance_SchemaSourceOfTruth(t *testing.T) {
	pg := schema.Schema(schema.DialectPostgres)
	sl := schema.Schema(schema.DialectSQLite)
	require.NotNil(t, pg, "schema.Schema(Postgres) returned nil")
	require.NotNil(t, sl, "schema.Schema(SQLite) returned nil")
	require.Lenf(t, pg.Tables, len(d1AcceptanceTablesPostgres),
		"postgres table count drifted; update d1AcceptanceTablesPostgres "+
			"in d1_helpers_test.go (want %d, got %d)",
		len(d1AcceptanceTablesPostgres), len(pg.Tables))
	require.Lenf(t, sl.Tables, len(d1AcceptanceTablesPostgres),
		"sqlite table count drifted; update d1AcceptanceTablesPostgres "+
			"in d1_helpers_test.go (want %d, got %d)",
		len(d1AcceptanceTablesPostgres), len(sl.Tables))

	// Per-dialect symmetric names — every table declared on Postgres
	// must exist on SQLite with the same name (PRD §6.6 portability).
	pgNames := make(map[string]struct{}, len(pg.Tables))
	for _, tbl := range pg.Tables {
		pgNames[tbl.Name] = struct{}{}
	}
	for _, tbl := range sl.Tables {
		_, ok := pgNames[tbl.Name]
		require.Truef(t, ok, "sqlite table %q has no postgres counterpart", tbl.Name)
	}
}

// TestD1_Acceptance_RuntimeUpDown covers PRD §D-1 bullet #5 —
// golang-migrate Up() then Down() succeeds on both backends. Down()
// brings the DB back to the pre-Up shape (no D-1 tables remain),
// validating that every .down.sql mirrors its .up.sql.
func TestD1_Acceptance_RuntimeUpDown(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "Up() failed on %s", b.name)

			// Sanity: at least one D-1 table exists post-Up.
			live := liveTableNames(t, ctx, db, b.name)
			require.NotEmptyf(t, live, "no D-1 tables visible post-Up on %s", b.name)

			require.NoError(t, m.Down(), "Down() failed on %s", b.name)
			liveAfter := liveTableNames(t, ctx, db, b.name)
			require.Emptyf(t, liveAfter,
				"Down() left tables behind on %s: %v", b.name, liveAfter)
		})
	}
}

// TestD1_Acceptance_DoubleUp covers PRD §D-1 bullet #9 — migrations are
// idempotent. Calling Up() a second time after a full Up() returns
// migrate.ErrNoChange (golang-migrate's "nothing to apply" signal),
// confirming that no .up.sql attempts to re-create or re-insert and
// that the schema_migrations tracker is consulted before re-running.
func TestD1_Acceptance_DoubleUp(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			_, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up(), "first Up() failed on %s", b.name)
			err := m.Up()
			require.ErrorIsf(t, err, migrate.ErrNoChange,
				"second Up() must report ErrNoChange on %s (got %v)", b.name, err)
		})
	}
}

// TestD1_Acceptance_TableInventory covers PRD §D-1 bullet #6 — after a
// full Up the live DB exposes the canonical 41 tables under the same
// names on both backends. d1AcceptanceTablesPostgres pins the expected
// set; ElementsMatch reports both missing and unexpected names.
func TestD1_Acceptance_TableInventory(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			live := liveTableNames(t, ctx, db, b.name)
			require.ElementsMatchf(t, d1AcceptanceTablesPostgres, live,
				"table inventory mismatch on %s", b.name)
		})
	}
}

// TestD1_Acceptance_ColumnInventory covers PRD §D-1 bullet #7 — every
// column declared in schema.go exists on the live DB with the declared
// name. Iterates every canonical table; per-table failures cite the
// table by name in the failure message.
func TestD1_Acceptance_ColumnInventory(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			d := schema.DialectPostgres
			if b.name == "sqlite" {
				d = schema.DialectSQLite
			}
			s := schema.Schema(d)
			for _, tbl := range s.Tables {
				want := columnNamesFromTable(tbl)
				got := liveColumnNames(t, ctx, db, b.name, tbl.Name)
				require.ElementsMatchf(t, want, got,
					"%s: column inventory mismatch on %s\nwant: %v\ngot: %v",
					tbl.Name, b.name, want, got)
			}
		})
	}
}

// TestD1_Acceptance_FKEnforcement covers PRD §D-1 bullet #8 — the live
// DB enforces FK constraints on both backends. SQLite enforcement is
// activated via the `_pragma=foreign_keys(1)` DSN clause set in
// openD1SQLite.
//
// Probe: insert a seasons row referencing a non-existent series_id —
// this exercises seasons_series_id_fkey (declared by buildSeasonsTable
// with ON DELETE NO ACTION ON UPDATE NO ACTION). Both backends MUST
// reject with a foreign-key-violation error. We deliberately avoid the
// app-managed instance_name FKs (series_cache.instance_name has no DB
// FK — cascade is app-managed in SonarrInstanceRepository.Delete).
func TestD1_Acceptance_FKEnforcement(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			_, err := db.ExecContext(ctx, insertSeasonBadFKSQL(b.name))
			require.Errorf(t, err, "FK should be enforced on %s", b.name)
			require.Truef(t, isFKViolation(err),
				"expected FK violation on %s, got: %v", b.name, err)
		})
	}
}

// TestD1_Acceptance_TmdbTypePartialIndex covers PRD §D-1 bullet #11 —
// series_tmdb_type_idx is a partial index ON (tmdb_type) WHERE
// tmdb_type IS NOT NULL. The definition is dialect-native (pg_indexes
// vs sqlite_master.sql) but both forms contain the predicate as
// lowercase "tmdb_type" + "where" + "not null".
func TestD1_Acceptance_TmdbTypePartialIndex(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			def := strings.ToLower(indexDefinition(t, ctx, db, b.name, "series_tmdb_type_idx"))
			require.Containsf(t, def, "tmdb_type",
				"%s: series_tmdb_type_idx missing tmdb_type column reference: %s",
				b.name, def)
			require.Containsf(t, def, "where",
				"%s: series_tmdb_type_idx missing WHERE clause (not partial?): %s",
				b.name, def)
			require.Containsf(t, def, "not null",
				"%s: series_tmdb_type_idx predicate not IS NOT NULL: %s",
				b.name, def)
		})
	}
}

// ---------- helpers private to the acceptance test ----------

// insertSeasonBadFKSQL returns a dialect-native INSERT into seasons with
// a non-existent series_id. The seasons_series_id_fkey FK MUST cause
// the INSERT to fail.
func insertSeasonBadFKSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO seasons (series_id, season_number, created_at, updated_at)
		        VALUES (9999999999, 1, now(), now())`
	case "sqlite":
		return `INSERT INTO seasons (series_id, season_number, created_at, updated_at)
		        VALUES (9999999999, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("insertSeasonBadFKSQL: unknown driver " + driver)
}

// isFKViolation returns true when err's message indicates a foreign-key
// violation on Postgres ("violates foreign key") or SQLite ("FOREIGN
// KEY constraint failed").
func isFKViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key") ||
		strings.Contains(msg, "violates")
}
