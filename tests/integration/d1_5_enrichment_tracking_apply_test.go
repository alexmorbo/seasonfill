//go:build integration

// D-1-5 (story 458) — verifies 000001..000008 apply cleanly on both
// backends, exercises insert + UNIQUE composite + polymorphic-no-FK +
// partial-index behavior on enrichment_errors.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD15_EnrichmentTrackingMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000008 in sequence.
			require.NoError(t, m.Up())

			// enrichment_errors happy path with explicit next_attempt_at.
			seedErr := "boom-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"series", int64(100), "tmdb", seedErr)
			require.NoError(t, err, "enrichment_errors insert should succeed")

			// UNIQUE composite-3 violation: same (entity_type, entity_id, source).
			_, err = db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"series", int64(100), "tmdb", "DUPE")
			require.Error(t, err, "duplicate (entity_type, entity_id, source) should fail")

			// Same (entity_type, entity_id), different source → OK.
			_, err = db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"series", int64(100), "omdb", "different-source")
			require.NoError(t, err, "different source for same entity should succeed")

			// Same (entity_type, source), different entity_id → OK.
			_, err = db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"series", int64(101), "tmdb", "different-entity")
			require.NoError(t, err, "different entity_id for same (type, source) should succeed")

			// POLYMORPHIC: orphan entity_id (no FK) should NOT fail.
			_, err = db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"episode", int64(9999999), "tmdb", "orphan-episode")
			require.NoError(t, err, "polymorphic enrichment_errors: orphan entity_id should NOT fail (no FK by design)")

			// Different entity_type with same numeric entity_id → OK.
			_, err = db.ExecContext(ctx, insertEnrichmentErrorSQL(b.name),
				"person", int64(100), "tmdb", "person-100")
			require.NoError(t, err, "different entity_type for same numeric entity_id should succeed")

			// Stamp next_attempt_at to exercise the partial-index query path.
			_, err = db.ExecContext(ctx, setNextAttemptSQL(b.name),
				"series", int64(100), "tmdb")
			require.NoError(t, err)

			// Read rows-ready-for-retry — exercises the partial index logically.
			cnt := countReadyForRetry(t, ctx, db, b.name)
			require.GreaterOrEqual(t, cnt, 1, "at least one enrichment_errors row should be ready for retry")

			// DOWN — rolls back 000008.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM enrichment_errors LIMIT 1")
			require.Error(t, err, "enrichment_errors should be dropped after Down")
		})
	}
}

func insertEnrichmentErrorSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO enrichment_errors (entity_type, entity_id, source, last_error)
		        VALUES ($1, $2, $3, $4)`
	case "sqlite":
		return `INSERT INTO enrichment_errors (entity_type, entity_id, source, last_error)
		        VALUES (?, ?, ?, ?)`
	}
	panic("unknown driver " + driver)
}

func setNextAttemptSQL(driver string) string {
	switch driver {
	case "postgres":
		return `UPDATE enrichment_errors SET next_attempt_at = now() + interval '1 hour'
		         WHERE entity_type = $1 AND entity_id = $2 AND source = $3`
	case "sqlite":
		return `UPDATE enrichment_errors SET next_attempt_at = datetime('now', '+1 hour')
		         WHERE entity_type = ? AND entity_id = ? AND source = ?`
	}
	panic("unknown driver " + driver)
}

func countReadyForRetry(t *testing.T, ctx context.Context, db *sql.DB, driver string) int {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT COUNT(*) FROM enrichment_errors WHERE next_attempt_at IS NOT NULL`
	case "sqlite":
		q = `SELECT COUNT(*) FROM enrichment_errors WHERE next_attempt_at IS NOT NULL`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var cnt int
	row := db.QueryRowContext(ctx, q)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}
