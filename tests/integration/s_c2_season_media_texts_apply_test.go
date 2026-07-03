//go:build integration

// S-C2 — verifies migrations apply cleanly through 000026 on both backends,
// exercises the season_media_texts composite PK + series FK, and rolls back clean.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func insertSeasonMediaTextSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO season_media_texts (series_id, season_number, language, poster_asset) VALUES ($1, $2, $3, $4)`
	case "sqlite":
		return `INSERT INTO season_media_texts (series_id, season_number, language, poster_asset) VALUES (?, ?, ?, ?)`
	}
	return ""
}

func TestSC2_SeasonMediaTextsMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			require.NoError(t, m.Up()) // applies through 000026

			seriesTitle := "s-c2-" + uuid.NewString()
			_, err := db.ExecContext(ctx, insertSeriesSQL(b.name), seriesTitle, "stub", false, "[]")
			require.NoError(t, err)
			var seriesID int64
			require.NoError(t, db.QueryRowContext(ctx, selectSeriesIDByTitleSQL(b.name), seriesTitle).Scan(&seriesID))
			require.Greater(t, seriesID, int64(0))

			// Happy insert.
			_, err = db.ExecContext(ctx, insertSeasonMediaTextSQL(b.name), seriesID, 1, "en-US", "/en.jpg")
			require.NoError(t, err, "season_media_texts insert should succeed")

			// Composite PK: dup (series_id, season_number, language) must fail.
			_, err = db.ExecContext(ctx, insertSeasonMediaTextSQL(b.name), seriesID, 1, "en-US", "/dupe.jpg")
			require.Error(t, err, "duplicate composite PK should violate PK")

			// Different season_number and different language are both fine.
			_, err = db.ExecContext(ctx, insertSeasonMediaTextSQL(b.name), seriesID, 2, "en-US", "/en2.jpg")
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, insertSeasonMediaTextSQL(b.name), seriesID, 1, "ru-RU", "/ru.jpg")
			require.NoError(t, err)

			// FK: orphan series_id must fail.
			_, err = db.ExecContext(ctx, insertSeasonMediaTextSQL(b.name), int64(999999), 1, "en-US", "/orphan.jpg")
			require.Error(t, err, "orphan season_media_texts should fail series FK")

			// DOWN — table gone.
			require.NoError(t, m.Down())
			_, err = db.ExecContext(ctx, "SELECT 1 FROM season_media_texts LIMIT 1")
			require.Error(t, err, "season_media_texts should be dropped after Down")
		})
	}
}
