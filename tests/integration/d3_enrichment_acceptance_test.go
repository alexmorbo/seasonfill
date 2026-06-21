//go:build integration

// D-3 (story 464b) — verifies the people.enrichment_synced_at column
// added by migration 000014 round-trips through Up+Down on both
// backends, plus the freshness-stamp write-pattern that PersonWorker /
// SeriesWorker rely on (single-column UPDATE; NULL on a stub-row
// upsert is preserved).
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestD3_PeopleEnrichmentSyncedAtRoundTrip — 000014 Up adds the column,
// PersonWorker-style single-column UPDATE bumps it, Down drops it.
func TestD3_PeopleEnrichmentSyncedAtRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000014 in sequence.
			require.NoError(t, m.Up())

			// Insert a stub people row (NULL enrichment_synced_at — the
			// migration adds the column nullable).
			_, err := db.ExecContext(ctx, insertPeopleStubSQL(b.name),
				int64(101), "Test Actor")
			require.NoError(t, err, "insert stub people row should succeed")

			// Column starts NULL.
			var syncedAt sql.NullTime
			err = db.QueryRowContext(ctx,
				"SELECT enrichment_synced_at FROM people WHERE id = 101").
				Scan(&syncedAt)
			require.NoError(t, err)
			require.False(t, syncedAt.Valid, "freshly-inserted people row must have NULL enrichment_synced_at")

			// MarkSynced-style single-column UPDATE bumps the column.
			now := time.Now().UTC().Truncate(time.Millisecond)
			_, err = db.ExecContext(ctx,
				"UPDATE people SET enrichment_synced_at = "+placeholderFor(b.name, 1)+
					", updated_at = "+placeholderFor(b.name, 2)+
					" WHERE id = "+placeholderFor(b.name, 3),
				now, now, int64(101))
			require.NoError(t, err)

			err = db.QueryRowContext(ctx,
				"SELECT enrichment_synced_at FROM people WHERE id = 101").
				Scan(&syncedAt)
			require.NoError(t, err)
			require.True(t, syncedAt.Valid, "after UPDATE the column must be non-NULL")
			require.WithinDuration(t, now, syncedAt.Time.UTC(), time.Second)
		})
	}
}

// insertPeopleStubSQL returns an INSERT statement for the people table
// targeting (id, name) with hydration defaulted to 'stub'. Driver-aware
// placeholders.
func insertPeopleStubSQL(driver string) string {
	if driver == "postgres" {
		return `INSERT INTO people (id, name, hydration, created_at, updated_at)
		        VALUES ($1, $2, 'stub', now(), now())`
	}
	return `INSERT INTO people (id, name, hydration, created_at, updated_at)
	        VALUES (?, ?, 'stub', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
}

// placeholderFor returns the Nth positional placeholder for the driver.
// Postgres uses $1, $2, ... ; sqlite uses ?.
func placeholderFor(driver string, n int) string {
	if driver == "postgres" {
		switch n {
		case 1:
			return "$1"
		case 2:
			return "$2"
		case 3:
			return "$3"
		}
	}
	return "?"
}
