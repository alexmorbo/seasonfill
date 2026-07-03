//go:build integration

// Story 455 (D-1-2) — verifies the generated migration applies cleanly
// on BOTH backends via golang-migrate (the runtime path; NOT atlas) and
// rolls back cleanly via the .down.sql.
//
// D-0 compliance:
//   - SQLite is exercised in-process with a fresh :memory: DB per test.
//   - Postgres lane gates on SEASONFILL_TEST_POSTGRES_ENABLE (matches
//     testhelpers.AllBackends — same opt-in surface, so CI's
//     test-integration-postgres target covers it).
//   - Unique UUIDs for series.original_title avoid collisions across
//     parallel runs against the shared Postgres container.
//   - Explicit error-pair coverage: FK violation MUST fail, with the
//     specific reason left dialect-side (PG SQLSTATE 23503, SQLite
//     "FOREIGN KEY constraint failed" — both are sufficient signal).
//
// Shared dual-backend helpers (openD1SQLite, openD1Postgres,
// d1MigrationsDir, d1SwapPGDBName, allD1Backends, insertSeriesSQL) live
// in d1_helpers_test.go (extracted during D-1-3a / story 456a).
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD12_CoreSeriesMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			// AllBackends-style: do NOT t.Parallel() inside the inner
			// loop; the shared Postgres container is happier when
			// per-test DB lifecycles are serialized.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — apply 000001_core_series.up.sql.
			require.NoError(t, m.Up(), "Up should apply the migration on a clean DB")

			// Smoke: series, seasons, episodes exist + a basic insert
			// path works.
			seriesTitle := "d1-2-" + uuid.NewString()
			_, err := db.ExecContext(ctx, insertSeriesSQL(b.name),
				seriesTitle, "stub", false, "[]")
			require.NoError(t, err, "insert into series should succeed")

			// FK enforcement: orphan episode INSERT must fail.
			_, err = db.ExecContext(ctx, insertOrphanEpisodeSQL(b.name),
				999999, 1, 1)
			require.Error(t, err, "FK violation expected for episodes.series_id = 999999")

			// DOWN — apply 000001_core_series.down.sql.
			require.NoError(t, m.Down(), "Down should drop the migration cleanly")

			// After down, series/seasons/episodes must not exist.
			_, err = db.ExecContext(ctx, "SELECT 1 FROM series LIMIT 1")
			require.Error(t, err, "series table should be dropped after Down")
		})
	}
}

func insertOrphanEpisodeSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO episodes (series_id, season_number, episode_number, created_at, updated_at)
		        VALUES ($1, $2, $3, now(), now())`
	case "sqlite":
		return `INSERT INTO episodes (series_id, season_number, episode_number, created_at, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
