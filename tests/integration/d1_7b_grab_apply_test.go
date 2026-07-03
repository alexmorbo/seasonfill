//go:build integration

// D-1-7b (story 460b) — verifies 000001..000012 apply cleanly on both
// backends, exercises CHECK constraints on grab_records.status +
// download_links (type_id_check, source_check), FK CASCADE on
// grab_records.instance_name and episode_grabs (dual), and the
// SET NULL FK on download_links.global_series_id.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestD1_7b_GrabMigrationApply applies 000001..000012 then exercises a
// 3-table happy path: insert sonarr_instance + series + episode + grab +
// episode_grab + download_link.
func TestD1_7b_GrabMigrationApply(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			// Verify the 3 grab tables exist.
			for _, tbl := range []string{"grab_records", "episode_grabs", "download_links"} {
				_, err := db.ExecContext(ctx, "SELECT 1 FROM "+tbl+" LIMIT 1")
				require.NoErrorf(t, err, "%s should exist after Up", tbl)
			}

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://sonarr.example.com")
			require.NoError(t, err)

			seriesTitle := "d1-7b-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertSeriesSQL(b.name),
				seriesTitle, "stub", false, "[]")
			require.NoError(t, err)
			seriesID := scanSeriesIDByTitle(t, ctx, db, b.name, seriesTitle)

			_, err = db.ExecContext(ctx, insertEpisodeMinimalSQL(b.name),
				seriesID, 1, 1)
			require.NoError(t, err)
			episodeID := scanEpisodeIDBySeriesSeasonEpisode(t, ctx, db, b.name,
				seriesID, 1, 1)

			grabID := "grab-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertGrabRecordMinimalSQL(b.name),
				grabID, instance, int64(42), 1)
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertEpisodeGrabSQL(b.name),
				grabID, episodeID, 1)
			require.NoError(t, err)

			qbitHash := "hash-" + uuid.NewString()[:32]
			_, err = db.ExecContext(ctx, insertDownloadLinkSonarrSQL(b.name),
				qbitHash, instance, int64(42), "webhook")
			require.NoError(t, err)

			var n int
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM episode_grabs WHERE grab_id = "+placeholder(b.name, 1),
				grabID).Scan(&n))
			require.Equal(t, 1, n, "episode_grabs link round-trip should yield 1 row")
		})
	}
}

// TestD1_7b_GrabRecordsStatusCheck — INSERT with bogus status fails the
// status_check.
func TestD1_7b_GrabRecordsStatusCheck(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertGrabRecordWithStatusSQL(b.name),
				"grab-"+uuid.NewString(), instance, int64(42), 1, "WAT")
			require.Error(t, err, "status='WAT' must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation on bad status, got %T: %v", err, err)
		})
	}
}

// TestD1_7b_GrabRecordsDefaultStatus — default 'grabbed' applies when
// status column is omitted. 467a / D-6 corrected the enum to match the
// domain (was 'pending' under the legacy unobserved drift).
func TestD1_7b_GrabRecordsDefaultStatus(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			grabID := "grab-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertGrabRecordMinimalSQL(b.name),
				grabID, instance, int64(42), 1)
			require.NoError(t, err)

			var status string
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT status FROM grab_records WHERE id = "+placeholder(b.name, 1),
				grabID).Scan(&status))
			require.Equal(t, "grabbed", status)
		})
	}
}

// TestD1_7b_EpisodeGrabsCascadeFromGrab — deleting the parent grab
// cascades the episode_grabs link rows.
func TestD1_7b_EpisodeGrabsCascadeFromGrab(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			seriesTitle := "d1-7b-cascade-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertSeriesSQL(b.name),
				seriesTitle, "stub", false, "[]")
			require.NoError(t, err)
			seriesID := scanSeriesIDByTitle(t, ctx, db, b.name, seriesTitle)
			_, err = db.ExecContext(ctx, insertEpisodeMinimalSQL(b.name),
				seriesID, 1, 1)
			require.NoError(t, err)
			episodeID := scanEpisodeIDBySeriesSeasonEpisode(t, ctx, db, b.name,
				seriesID, 1, 1)

			grabID := "grab-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertGrabRecordMinimalSQL(b.name),
				grabID, instance, int64(42), 1)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, insertEpisodeGrabSQL(b.name),
				grabID, episodeID, 1)
			require.NoError(t, err)

			// DELETE the parent grab — child link row should vanish.
			_, err = db.ExecContext(ctx,
				"DELETE FROM grab_records WHERE id = "+placeholder(b.name, 1), grabID)
			require.NoError(t, err)
			var n int
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM episode_grabs WHERE grab_id = "+placeholder(b.name, 1),
				grabID).Scan(&n))
			require.Equal(t, 0, n, "episode_grabs should CASCADE from grab DELETE")
		})
	}
}

// TestD1_7b_DownloadLinksTypeIDCheck — sonarr-without-series_id and
// both-ids-set both fail the type/id CHECK.
func TestD1_7b_DownloadLinksTypeIDCheck(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			// sonarr instance_type WITHOUT external_series_id → CHECK.
			_, err = db.ExecContext(ctx, insertDownloadLinkBareSQL(b.name),
				"hash-bad-"+uuid.NewString()[:8], instance, "sonarr", "webhook")
			require.Error(t, err, "sonarr without external_series_id must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation, got %T: %v", err, err)

			// sonarr with BOTH series_id and movie_id set → CHECK.
			_, err = db.ExecContext(ctx, insertDownloadLinkBothIDsSQL(b.name),
				"hash-both-"+uuid.NewString()[:8], instance, "sonarr",
				int64(1), int64(1), "webhook")
			require.Error(t, err, "sonarr with both ids must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation, got %T: %v", err, err)
		})
	}
}

// TestD1_7b_DownloadLinksSourceCheck — bogus source value rejected.
func TestD1_7b_DownloadLinksSourceCheck(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertDownloadLinkSonarrSQL(b.name),
				"hash-bs-"+uuid.NewString()[:8], instance, int64(42), "manual")
			require.Error(t, err, "source='manual' must hit CHECK")
			require.Truef(t, isCheckViolation(b.name, err),
				"expected CHECK violation, got %T: %v", err, err)
		})
	}
}

// TestD1_7b_DownloadLinksGlobalSeriesSetNull — deleting the referenced
// canonical series sets download_links.global_series_id NULL rather
// than dropping the row.
func TestD1_7b_DownloadLinksGlobalSeriesSetNull(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://s.example.com")
			require.NoError(t, err)

			seriesTitle := "d1-7b-setnull-" + uuid.NewString()
			_, err = db.ExecContext(ctx, insertSeriesSQL(b.name),
				seriesTitle, "stub", false, "[]")
			require.NoError(t, err)
			seriesID := scanSeriesIDByTitle(t, ctx, db, b.name, seriesTitle)

			qbitHash := "hash-link-" + uuid.NewString()[:8]
			_, err = db.ExecContext(ctx, insertDownloadLinkSonarrWithGlobalSQL(b.name),
				qbitHash, instance, int64(42), seriesID, "webhook")
			require.NoError(t, err)

			// DELETE series → global_series_id should go NULL.
			_, err = db.ExecContext(ctx,
				"DELETE FROM series WHERE id = "+placeholder(b.name, 1), seriesID)
			require.NoError(t, err)
			var gsi sql.NullInt64
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT global_series_id FROM download_links WHERE qbit_hash = "+placeholder(b.name, 1),
				qbitHash).Scan(&gsi))
			require.False(t, gsi.Valid, "global_series_id should be NULL after series DELETE")
		})
	}
}

// ---------- SQL builders / helpers ----------

func placeholder(driver string, n int) string {
	if driver == "postgres" {
		// support up to a couple of dozen substitutions for these tests
		switch n {
		case 1:
			return "$1"
		case 2:
			return "$2"
		case 3:
			return "$3"
		case 4:
			return "$4"
		case 5:
			return "$5"
		}
		return "$1"
	}
	return "?"
}

func insertEpisodeMinimalSQL(driver string) string {
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

func scanSeriesIDByTitle(t *testing.T, ctx context.Context, db *sql.DB, driver, title string) int64 {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT id FROM series WHERE original_title = $1`
	case "sqlite":
		q = `SELECT id FROM series WHERE original_title = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var id int64
	require.NoError(t, db.QueryRowContext(ctx, q, title).Scan(&id))
	return id
}

func scanEpisodeIDBySeriesSeasonEpisode(t *testing.T, ctx context.Context, db *sql.DB,
	driver string, seriesID int64, seasonNumber, episodeNumber int,
) int64 {
	t.Helper()
	var q string
	switch driver {
	case "postgres":
		q = `SELECT id FROM episodes WHERE series_id = $1 AND season_number = $2 AND episode_number = $3`
	case "sqlite":
		q = `SELECT id FROM episodes WHERE series_id = ? AND season_number = ? AND episode_number = ?`
	default:
		t.Fatalf("unknown driver %q", driver)
	}
	var id int64
	require.NoError(t, db.QueryRowContext(ctx, q, seriesID, seasonNumber, episodeNumber).Scan(&id))
	return id
}

func insertGrabRecordMinimalSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO grab_records
		        (id, instance_name, series_id, season_number, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, now(), now())`
	case "sqlite":
		return `INSERT INTO grab_records
		        (id, instance_name, series_id, season_number, created_at, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertGrabRecordWithStatusSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO grab_records
		        (id, instance_name, series_id, season_number, status, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, $5, now(), now())`
	case "sqlite":
		return `INSERT INTO grab_records
		        (id, instance_name, series_id, season_number, status, created_at, updated_at)
		        VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertEpisodeGrabSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO episode_grabs
		        (grab_id, episode_id, episode_number, created_at, updated_at)
		        VALUES ($1, $2, $3, now(), now())`
	case "sqlite":
		return `INSERT INTO episode_grabs
		        (grab_id, episode_id, episode_number, created_at, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDownloadLinkSonarrSQL — sonarr-source, external_series_id set,
// external_movie_id NULL (passes type_id CHECK).
func insertDownloadLinkSonarrSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id, source,
		         created_at, updated_at, discovered_at)
		        VALUES ($1, $2, 'sonarr', $3, $4, now(), now(), now())`
	case "sqlite":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id, source,
		         created_at, updated_at, discovered_at)
		        VALUES (?, ?, 'sonarr', ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDownloadLinkSonarrWithGlobalSQL — same as
// insertDownloadLinkSonarrSQL but also sets global_series_id.
func insertDownloadLinkSonarrWithGlobalSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id,
		         global_series_id, source, created_at, updated_at, discovered_at)
		        VALUES ($1, $2, 'sonarr', $3, $4, $5, now(), now(), now())`
	case "sqlite":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id,
		         global_series_id, source, created_at, updated_at, discovered_at)
		        VALUES (?, ?, 'sonarr', ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDownloadLinkBareSQL — instance_type set but no external_*_id;
// used to trigger the type_id_check.
func insertDownloadLinkBareSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, source,
		         created_at, updated_at, discovered_at)
		        VALUES ($1, $2, $3, $4, now(), now(), now())`
	case "sqlite":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, source,
		         created_at, updated_at, discovered_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertDownloadLinkBothIDsSQL — both external_series_id AND
// external_movie_id set; triggers the type_id_check.
func insertDownloadLinkBothIDsSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id,
		         external_movie_id, source, created_at, updated_at, discovered_at)
		        VALUES ($1, $2, $3, $4, $5, $6, now(), now(), now())`
	case "sqlite":
		return `INSERT INTO download_links
		        (qbit_hash, instance_name, instance_type, external_series_id,
		         external_movie_id, source, created_at, updated_at, discovered_at)
		        VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
