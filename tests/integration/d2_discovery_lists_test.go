//go:build integration

// D-2 / N-2a (story 502) — verifies 000001..000021 apply cleanly on
// both backends and exercises the discovery_lists curated-ranking table
// PRD §5.1.1.
//
// Reuses d1_helpers_test.go for the dual-backend migrate harness:
//
//	allD1Backends(t) / openD1SQLite / openD1Postgres   — dual backends
//	insertSeriesAndScanID(...)                          — parent fixture
//	insertSeriesSQL / deleteSeriesSQL                   — series CRUD
//	placeholderD14b                                     — driver placeholder
//
// Coverage matrix:
//
//  1. happy-path INSERT with the 4-tuple PK + position + refreshed_at
//     default writes against both Postgres + SQLite.
//  2. 3 sibling rows with the same `position` but distinct
//     (kind, param, language) — proves position is NOT part of the PK
//     and the 3 lists can coexist.
//  3. `param` DEFAULT ” coverage (trending+popular case).
//  4. composite PK violation — duplicate (kind, param, language, series_id)
//     → expect SQLSTATE 23505 on Postgres / UNIQUE constraint failed on SQLite.
//  5. NOT NULL on `kind` — INSERT with NULL kind → error.
//  6. FK CASCADE — DELETE parent series → discovery_lists rows vanish.
//  7. FK violation — INSERT with orphan series_id → error.
//  8. DOWN — both directions clean up the table.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestD2_DiscoveryListsApply(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000021 in sequence.
			require.NoError(t, m.Up())

			// Seed a series row to satisfy FK targets.
			seriesTitle := "d2-disc-" + uuid.NewString()
			seriesID := insertSeriesAndScanID(t, ctx, db, b.name, seriesTitle)

			// (1) happy-path INSERT — kind='trending_day', param defaulted to '',
			//     language='en-US', position=1.
			lang := "en-US-" + uuid.NewString()[:8] // unique language tag per backend
			_, err := db.ExecContext(ctx, insertDiscoveryListSQL(b.name),
				"trending_day", "", lang, seriesID, 1)
			require.NoError(t, err, "discovery_lists insert (trending_day) should succeed")

			// (2) sibling INSERTs with the same `position` but distinct
			//     (kind, param, language) tuples.
			_, err = db.ExecContext(ctx, insertDiscoveryListSQL(b.name),
				"popular", "", lang, seriesID, 1)
			require.NoError(t, err, "discovery_lists insert (popular) should succeed")

			_, err = db.ExecContext(ctx, insertDiscoveryListSQL(b.name),
				"by_genre", "28", lang, seriesID, 1)
			require.NoError(t, err, "discovery_lists insert (by_genre param=28) should succeed")

			require.Equal(t, 3, countDiscoveryListsForSeries(t, ctx, db, b.name, seriesID),
				"3 sibling rows with same position but distinct (kind,param,language) should coexist")

			// (3) `param` DEFAULT '' coverage — INSERT omitting param column.
			_, err = db.ExecContext(ctx, insertDiscoveryListNoParamSQL(b.name),
				"trending_week", lang, seriesID, 1)
			require.NoError(t, err, "discovery_lists insert without param (DEFAULT '') should succeed")
			require.Equal(t, 4, countDiscoveryListsForSeries(t, ctx, db, b.name, seriesID))

			// (4) composite-PK violation — duplicate (trending_day, '', lang, seriesID).
			_, err = db.ExecContext(ctx, insertDiscoveryListSQL(b.name),
				"trending_day", "", lang, seriesID, 99)
			require.Error(t, err, "duplicate composite PK should fail")
			if b.name == "postgres" {
				// Strong assertion: SQLSTATE 23505 (unique_violation).
				var pgErr *pgconn.PgError
				require.ErrorAs(t, err, &pgErr,
					"expected *pgconn.PgError on duplicate composite PK")
				require.Equal(t, "23505", pgErr.Code,
					"want SQLSTATE 23505 unique_violation; got %s (%s)", pgErr.Code, pgErr.Message)
			} else {
				require.True(t,
					strings.Contains(strings.ToLower(err.Error()), "unique"),
					"want UNIQUE constraint message on SQLite; got %v", err)
			}

			// (5) NOT NULL on `kind` — pass empty kind via parameter binding.
			//     SQL "" passes NOT NULL — to exercise NULL we use a separate SQL.
			_, err = db.ExecContext(ctx, insertDiscoveryListNullKindSQL(b.name),
				"", lang, seriesID, 1)
			require.Error(t, err, "NULL kind should fail NOT NULL constraint")

			// (6) FK violation — orphan series_id.
			_, err = db.ExecContext(ctx, insertDiscoveryListSQL(b.name),
				"trending_day", "", lang, int64(9999999), 1)
			require.Error(t, err, "orphan series_id should fail FK")

			// (7) FK CASCADE — DELETE parent series wipes ALL discovery_lists rows
			//     pointing at it. Sanity-baseline count first.
			require.Equal(t, 4, countDiscoveryListsForSeries(t, ctx, db, b.name, seriesID))

			_, err = db.ExecContext(ctx, deleteSeriesSQL(b.name), seriesID)
			require.NoError(t, err)

			require.Equal(t, 0, countDiscoveryListsForSeries(t, ctx, db, b.name, seriesID),
				"FK CASCADE should have removed all discovery_lists rows for series_id=%d", seriesID)

			// (8) DOWN — rolls back 000021 then earlier migrations cleanly.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM discovery_lists LIMIT 1")
			require.Error(t, err, "discovery_lists should be dropped after Down")
		})
	}
}

// insertDiscoveryListSQL writes a fully-populated row with all 6 columns
// bound (refreshed_at defaulted via NOW()/CURRENT_TIMESTAMP).
func insertDiscoveryListSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES ($1, $2, $3, $4, $5, now())`
	case "sqlite":
		return `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDiscoveryListNoParamSQL omits the `param` column to exercise its
// DEFAULT ” literal on both dialects.
func insertDiscoveryListNoParamSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO discovery_lists (kind, language, series_id, position, refreshed_at)
		        VALUES ($1, $2, $3, $4, now())`
	case "sqlite":
		return `INSERT INTO discovery_lists (kind, language, series_id, position, refreshed_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDiscoveryListNullKindSQL injects a literal NULL for `kind` (the
// caller's empty-string arg is ignored) to exercise the NOT NULL
// constraint. Param binding cannot send a SQL NULL via empty string
// parameter — the driver would send the empty literal, which passes
// NOT NULL.
func insertDiscoveryListNullKindSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES (NULL, $1, $2, $3, $4, now())`
	case "sqlite":
		return `INSERT INTO discovery_lists (kind, param, language, series_id, position, refreshed_at)
		        VALUES (NULL, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// countDiscoveryListsForSeries returns COUNT(*) for the parent series.
// Used to baseline FK CASCADE behavior.
func countDiscoveryListsForSeries(t *testing.T, ctx context.Context, db *sql.DB, driver string, seriesID int64) int {
	t.Helper()
	q := fmt.Sprintf("SELECT COUNT(*) FROM discovery_lists WHERE series_id = %s",
		placeholderD14b(driver, 1))
	var cnt int
	row := db.QueryRowContext(ctx, q, seriesID)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}
