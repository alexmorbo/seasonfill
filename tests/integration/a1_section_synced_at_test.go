//go:build integration

// Story E-1-A1 — section_synced_at migration acceptance.
//
// Verifies: 4 new series columns + 1 new seasons column applied; backfill
// from enrichment_tmdb_synced_at runs (F-R2-4); stub rows stay NULL.
// Runs against BOTH SQLite (in-memory) and Postgres (testcontainers per
// SEASONFILL_TEST_POSTGRES_ENABLE opt-in) via the dual-backend matrix
// helpers in d1_helpers_test.go.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestA1SectionSyncedAt_BackfillCorrect — F-R2-4 backfill source check.
// Pre-migration row with enrichment_tmdb_synced_at set must end up with
// the 4 new section stamps copied. Stub row (NULL) stays NULL.
func TestA1SectionSyncedAt_BackfillCorrect(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// Step pre-A1 to migration 000021. Seed sample rows. Then
			// run Up once more to apply 000022 backfill.
			require.NoError(t, m.Migrate(21), "migrate to 000021 should succeed")

			ts := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			fullTitle := "a1-full-" + uuid.NewString()
			stubTitle := "a1-stub-" + uuid.NewString()

			switch b.name {
			case "postgres":
				_, err := db.ExecContext(ctx,
					`INSERT INTO series (hydration, title, enrichment_tmdb_synced_at, origin_countries, created_at, updated_at)
					 VALUES ($1, $2, $3, '[]', now(), now())`,
					"full", fullTitle, ts)
				require.NoError(t, err)
				_, err = db.ExecContext(ctx,
					`INSERT INTO series (hydration, title, origin_countries, created_at, updated_at)
					 VALUES ($1, $2, '[]', now(), now())`,
					"stub", stubTitle)
				require.NoError(t, err)
			case "sqlite":
				_, err := db.ExecContext(ctx,
					`INSERT INTO series (hydration, title, enrichment_tmdb_synced_at, origin_countries, created_at, updated_at)
					 VALUES (?, ?, ?, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
					"full", fullTitle, ts)
				require.NoError(t, err)
				_, err = db.ExecContext(ctx,
					`INSERT INTO series (hydration, title, origin_countries, created_at, updated_at)
					 VALUES (?, ?, '[]', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
					"stub", stubTitle)
				require.NoError(t, err)
			}

			// Step into 000022 — runs the backfill.
			require.NoError(t, m.Migrate(22), "migrate to 000022 should succeed")

			// Full row got backfilled with the tmdb stamp.
			selectSQL := selectSectionSyncedAtByTitle(b.name)
			var stamp *time.Time
			require.NoError(t,
				db.QueryRowContext(ctx, selectSQL, fullTitle).Scan(&stamp))
			require.NotNil(t, stamp, "full row enrichment_text_synced_at must be backfilled")
			assert.WithinDuration(t, ts, *stamp, time.Second)

			// Stub row stays NULL.
			require.NoError(t,
				db.QueryRowContext(ctx, selectSQL, stubTitle).Scan(&stamp))
			assert.Nil(t, stamp, "stub row enrichment_text_synced_at must stay NULL")
		})
	}
}

// TestA1SectionSyncedAt_ColumnsExist — sanity check on column presence.
// SQLite-only — pragma_table_info is portable enough for the cheap check.
func TestA1SectionSyncedAt_ColumnsExist(t *testing.T) {
	db, m, cleanup := openD1SQLite(t)
	t.Cleanup(cleanup)
	require.NoError(t, m.Up())

	for _, col := range []string{
		"enrichment_text_synced_at",
		"enrichment_cast_synced_at",
		"enrichment_recs_synced_at",
		"enrichment_media_synced_at",
	} {
		var present int
		require.NoError(t,
			db.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info('series') WHERE name = ?`, col,
			).Scan(&present))
		assert.Equal(t, 1, present, "series.%s missing", col)
	}

	var seasonsCol int
	require.NoError(t,
		db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('seasons') WHERE name = 'episodes_synced_at'`,
		).Scan(&seasonsCol))
	assert.Equal(t, 1, seasonsCol, "seasons.episodes_synced_at missing")
}

// selectSectionSyncedAtByTitle returns dialect-specific SELECT picking
// up enrichment_text_synced_at for a row identified by its (unique-for-
// this-test) title.
func selectSectionSyncedAtByTitle(driver string) string {
	switch driver {
	case "postgres":
		return `SELECT enrichment_text_synced_at FROM series WHERE title = $1`
	case "sqlite":
		return `SELECT enrichment_text_synced_at FROM series WHERE title = ?`
	}
	panic("unknown driver " + driver)
}
