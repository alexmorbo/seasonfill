//go:build integration

// D-1-4b (story 457b) — verifies 000001..000006 apply cleanly on both
// backends, exercises insert + FK + composite PK + cascade behavior on
// the 4 new series-extras tables (videos, content_ratings, external_ids,
// series_recommendations), AND the polymorphic no-FK invariant on
// external_ids.
package integration

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestD14b_SeriesExtrasMigrationRoundTrip(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — applies 000001..000006 in sequence.
			require.NoError(t, m.Up())

			// Seed a series row to satisfy FK targets.
			seriesTitle := "d1-4b-" + uuid.NewString()
			seriesID := insertSeriesAndScanID(t, ctx, db, b.name, seriesTitle)

			// videos happy path.
			_, err := db.ExecContext(ctx, insertVideoSQL(b.name),
				seriesID, "vid-"+uuid.NewString(), "Trailer 1", "YouTube", "trailer", true)
			require.NoError(t, err, "videos insert should succeed")

			// content_ratings happy path + composite PK violation.
			_, err = db.ExecContext(ctx, insertContentRatingSQL(b.name),
				seriesID, "US", "TV-MA")
			require.NoError(t, err, "content_ratings insert should succeed")
			_, err = db.ExecContext(ctx, insertContentRatingSQL(b.name),
				seriesID, "US", "TV-14")
			require.Error(t, err, "duplicate (series_id, country_code) should fail")

			// content_ratings FK violation: orphan series_id.
			_, err = db.ExecContext(ctx, insertContentRatingSQL(b.name),
				int64(9999999), "GB", "TV-PG")
			require.Error(t, err, "orphan content_ratings series_id should fail FK")

			// external_ids happy path + composite-3 PK violation.
			_, err = db.ExecContext(ctx, insertExternalIDSQL(b.name),
				"series", seriesID, "imdb", "tt1234567")
			require.NoError(t, err, "external_ids insert should succeed")
			_, err = db.ExecContext(ctx, insertExternalIDSQL(b.name),
				"series", seriesID, "imdb", "DUPE")
			require.Error(t, err, "duplicate (entity_type, entity_id, provider) should fail")

			// Same (entity_type, entity_id) but different provider — OK.
			_, err = db.ExecContext(ctx, insertExternalIDSQL(b.name),
				"series", seriesID, "tvdb", "tvdb12345")
			require.NoError(t, err, "different provider for same entity should succeed")

			// POLYMORPHIC: orphan external_ids (entity_id=9999999, no FK) SUCCEEDS.
			_, err = db.ExecContext(ctx, insertExternalIDSQL(b.name),
				"person", int64(9999999), "imdb", "tt9999999")
			require.NoError(t, err, "polymorphic external_ids: orphan entity_id should NOT fail (no FK by design)")

			// Seed a 2nd series so recommendations has a valid recommended_series_id.
			seriesTitle2 := "d1-4b-rec-" + uuid.NewString()
			seriesID2 := insertSeriesAndScanID(t, ctx, db, b.name, seriesTitle2)

			// series_recommendations happy path.
			_, err = db.ExecContext(ctx, insertRecommendationSQL(b.name),
				seriesID, seriesID2, 0)
			require.NoError(t, err, "series_recommendations insert should succeed")

			// Composite PK violation: same (series_id, recommended_series_id).
			_, err = db.ExecContext(ctx, insertRecommendationSQL(b.name),
				seriesID, seriesID2, 5)
			require.Error(t, err, "duplicate (series_id, recommended_series_id) should fail")

			// FK violation: orphan recommended_series_id.
			_, err = db.ExecContext(ctx, insertRecommendationSQL(b.name),
				seriesID, int64(9999999), 1)
			require.Error(t, err, "orphan recommended_series_id should fail FK")

			// FK violation: orphan series_id.
			_, err = db.ExecContext(ctx, insertRecommendationSQL(b.name),
				int64(9999999), seriesID2, 1)
			require.Error(t, err, "orphan series_id should fail FK")

			// videos baseline count for seriesID before delete.
			require.Equal(t, 1, countWhereSeriesID(t, ctx, db, b.name, "videos", seriesID))
			require.Equal(t, 1, countWhereSeriesID(t, ctx, db, b.name, "content_ratings", seriesID))
			require.Equal(t, 1, countWhereSeriesID(t, ctx, db, b.name, "series_recommendations", seriesID))

			// Cascade: DELETE series → videos/content_ratings/recommendations rows for that series removed.
			_, err = db.ExecContext(ctx, deleteSeriesSQL(b.name), seriesID)
			require.NoError(t, err)

			require.Equal(t, 0, countWhereSeriesID(t, ctx, db, b.name, "videos", seriesID),
				"cascade should have removed videos rows for series_id=%d", seriesID)
			require.Equal(t, 0, countWhereSeriesID(t, ctx, db, b.name, "content_ratings", seriesID),
				"cascade should have removed content_ratings rows for series_id=%d", seriesID)
			require.Equal(t, 0, countWhereSeriesID(t, ctx, db, b.name, "series_recommendations", seriesID),
				"cascade should have removed series_recommendations rows for series_id=%d", seriesID)

			// NOTE: external_ids row for seriesID is NOT removed by series delete
			// (no FK by design — polymorphic). Verify it stuck around.
			extCnt := countExternalIDsForEntity(t, ctx, db, b.name, "series", seriesID)
			require.Equal(t, 2, extCnt,
				"external_ids should NOT be cascaded by series delete (polymorphic); want 2 surviving rows for entity_id=%d", seriesID)

			// DOWN — rolls back 000006 then earlier migrations.
			require.NoError(t, m.Down())
			for _, table := range []string{"videos", "content_ratings", "external_ids", "series_recommendations"} {
				_, err = db.ExecContext(ctx, "SELECT 1 FROM "+table+" LIMIT 1")
				require.Errorf(t, err, "%s should be dropped after Down", table)
			}
		})
	}
}

func insertVideoSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO videos (series_id, tmdb_video_id, name, site, type, official, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, $5, $6, now(), now())`
	case "sqlite":
		return `INSERT INTO videos (series_id, tmdb_video_id, name, site, type, official, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertContentRatingSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO content_ratings (series_id, country_code, rating, updated_at)
		        VALUES ($1, $2, $3, now())`
	case "sqlite":
		return `INSERT INTO content_ratings (series_id, country_code, rating, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertExternalIDSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO external_ids (entity_type, entity_id, provider, value, updated_at)
		        VALUES ($1, $2, $3, $4, now())`
	case "sqlite":
		return `INSERT INTO external_ids (entity_type, entity_id, provider, value, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertRecommendationSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO series_recommendations (series_id, recommended_series_id, position, updated_at)
		        VALUES ($1, $2, $3, now())`
	case "sqlite":
		return `INSERT INTO series_recommendations (series_id, recommended_series_id, position, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func deleteSeriesSQL(driver string) string {
	switch driver {
	case "postgres":
		return `DELETE FROM series WHERE id = $1`
	case "sqlite":
		return `DELETE FROM series WHERE id = ?`
	}
	panic("unknown driver " + driver)
}

func countWhereSeriesID(t *testing.T, ctx context.Context, db *sql.DB, driver, table string, seriesID int64) int {
	t.Helper()
	q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE series_id = %s", table, placeholderD14b(driver, 1))
	var cnt int
	row := db.QueryRowContext(ctx, q, seriesID)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}

func countExternalIDsForEntity(t *testing.T, ctx context.Context, db *sql.DB, driver, entityType string, entityID int64) int {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT COUNT(*) FROM external_ids WHERE entity_type = $1 AND entity_id = $2`
	case "sqlite":
		q = `SELECT COUNT(*) FROM external_ids WHERE entity_type = ? AND entity_id = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var cnt int
	row := db.QueryRowContext(ctx, q, entityType, entityID)
	require.NoError(t, row.Scan(&cnt))
	return cnt
}

func placeholderD14b(driver string, n int) string {
	switch driver {
	case "postgres":
		return fmt.Sprintf("$%d", n)
	case "sqlite":
		return "?"
	}
	panic("unknown driver " + driver)
}
