//go:build integration

// D-1-6a (story 459a) — verifies 000001..000009 apply cleanly on both
// backends, exercises insert + FK CASCADE + UNIQUE composite-4 +
// language="" neutral behavior on series_images.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD16a_SeriesImagesMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000009 in sequence.
			require.NoError(t, m.Up())

			// Seed a series row to satisfy FK target.
			seriesTitle := "d1-6a-" + uuid.NewString()
			seriesID := insertSeriesAndScanID(t, ctx, db, b.name, seriesTitle)

			// series_images happy path — 3 poster positions in en-US.
			for pos := 0; pos < 3; pos++ {
				_, err := db.ExecContext(ctx, insertSeriesImageSQL(b.name),
					seriesID, "en-US", "poster", "/p"+uuid.NewString()[:8]+".jpg", pos)
				require.NoError(t, err, "series_images insert position=%d should succeed", pos)
			}

			// UNIQUE composite-4 violation: same (series, lang, kind, position).
			_, err := db.ExecContext(ctx, insertSeriesImageSQL(b.name),
				seriesID, "en-US", "poster", "/p-dupe.jpg", 0)
			require.Error(t, err, "duplicate (series_id, language, kind, position) should fail")

			// Different language same (kind, position) → OK.
			_, err = db.ExecContext(ctx, insertSeriesImageSQL(b.name),
				seriesID, "ru-RU", "poster", "/p-ru.jpg", 0)
			require.NoError(t, err, "different language same (series, kind, position) should succeed")

			// Different kind same (lang, position) → OK.
			_, err = db.ExecContext(ctx, insertSeriesImageSQL(b.name),
				seriesID, "en-US", "backdrop", "/b.jpg", 0)
			require.NoError(t, err, "different kind same (series, lang, position) should succeed")

			// language="" neutral-image insert succeeds (backdrop without overlay).
			_, err = db.ExecContext(ctx, insertSeriesImageSQL(b.name),
				seriesID, "", "backdrop", "/b-neutral.jpg", 0)
			require.NoError(t, err, "language=\"\" neutral-image insert should succeed")

			// FK violation: orphan series_id.
			_, err = db.ExecContext(ctx, insertSeriesImageSQL(b.name),
				int64(9999999), "en-US", "poster", "/orphan.jpg", 0)
			require.Error(t, err, "orphan series_id should fail FK")

			// Confirm the 6 rows we inserted (3 posters en-US + 1 ru-RU poster + 1 en-US backdrop + 1 neutral backdrop).
			require.Equal(t, 6, countSeriesImages(t, ctx, db, b.name, seriesID))

			// CASCADE: dropping the canon series should wipe all series_images for that series.
			_, err = db.ExecContext(ctx, deleteSeriesSQL(b.name), seriesID)
			require.NoError(t, err, "DELETE FROM series should succeed")
			require.Equal(t, 0, countSeriesImages(t, ctx, db, b.name, seriesID),
				"series_images rows should be CASCADE-deleted when canon series is dropped")

			// DOWN — rolls back 000009.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM series_images LIMIT 1")
			require.Error(t, err, "series_images should be dropped after Down")
		})
	}
}

func insertSeriesImageSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO series_images (series_id, language, kind, tmdb_path, position, updated_at)
		        VALUES ($1, $2, $3, $4, $5, now())`
	case "sqlite":
		return `INSERT INTO series_images (series_id, language, kind, tmdb_path, position, updated_at)
		        VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func countSeriesImages(t *testing.T, ctx context.Context, db *sql.DB, driver string, seriesID int64) int {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT COUNT(*) FROM series_images WHERE series_id = $1`
	case "sqlite":
		q = `SELECT COUNT(*) FROM series_images WHERE series_id = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var cnt int
	row := db.QueryRowContext(ctx, q, seriesID)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}
