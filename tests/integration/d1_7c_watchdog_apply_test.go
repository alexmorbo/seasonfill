//go:build integration

// D-1-7c (story 460c) — verifies 000001..000013 apply cleanly on both
// backends, exercises composite-PK uniqueness, FK CASCADE on
// watchdog_state.instance_name and watchdog_blacklist.instance_name,
// nullable column round-trip, and 000013 down idempotency.
package integration

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestD1_7c_WatchdogMigrationApply applies 000001..000013 then exercises
// a happy-path round-trip: insert sonarr_instance + watchdog_state row
// + watchdog_blacklist row; round-trip read both back.
func TestD1_7c_WatchdogMigrationApply(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			for _, tbl := range []string{"watchdog_state", "watchdog_blacklist"} {
				_, err := db.ExecContext(ctx, "SELECT 1 FROM "+tbl+" LIMIT 1")
				require.NoErrorf(t, err, "%s should exist after Up", tbl)
			}

			instance := "inst-" + uuid.NewString()[:8]
			_, err := db.ExecContext(ctx, insertSonarrInstanceSQL(b.name),
				instance, "https://sonarr.example.com")
			require.NoError(t, err)

			seriesID := int64(123)
			season := 4

			_, err = db.ExecContext(ctx, insertWatchdogStateSQL(b.name),
				instance, seriesID, season, 1, "some error")
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertWatchdogBlacklistSQL(b.name),
				instance, seriesID, season, "Some.Release.1080p", "max_consecutive_no_better", 3)
			require.NoError(t, err)

			var attemptCount int
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT attempt_count FROM watchdog_state WHERE instance_name = `+
					placeholder(b.name, 1)+` AND sonarr_series_id = `+placeholder(b.name, 2)+
					` AND season_number = `+placeholder(b.name, 3),
				instance, seriesID, season).Scan(&attemptCount))
			require.Equal(t, 1, attemptCount)

			var reason string
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT reason FROM watchdog_blacklist WHERE instance_name = `+
					placeholder(b.name, 1)+` AND sonarr_series_id = `+placeholder(b.name, 2)+
					` AND season_number = `+placeholder(b.name, 3),
				instance, seriesID, season).Scan(&reason))
			require.Equal(t, "max_consecutive_no_better", reason)
		})
	}
}

// TestD1_7c_WatchdogState_CompositePKViolation — INSERT two rows with
// the same (instance_name, sonarr_series_id, season_number) → second
// fails on PK conflict.
func TestD1_7c_WatchdogState_CompositePKViolation(t *testing.T) {
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

			_, err = db.ExecContext(ctx, insertWatchdogStateSQL(b.name),
				instance, int64(42), 1, 0, "")
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertWatchdogStateSQL(b.name),
				instance, int64(42), 1, 0, "")
			require.Error(t, err, "duplicate triple should fail on PK")
			require.Truef(t, isUniqueViolation(b.name, err),
				"expected UNIQUE/PK violation, got %T: %v", err, err)
		})
	}
}

// TestD1_7c_WatchdogBlacklist_CompositePKViolation — same shape for blacklist.
func TestD1_7c_WatchdogBlacklist_CompositePKViolation(t *testing.T) {
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

			_, err = db.ExecContext(ctx, insertWatchdogBlacklistSQL(b.name),
				instance, int64(42), 1, "rel-a", "reason-a", 0)
			require.NoError(t, err)

			_, err = db.ExecContext(ctx, insertWatchdogBlacklistSQL(b.name),
				instance, int64(42), 1, "rel-b", "reason-b", 0)
			require.Error(t, err, "duplicate triple should fail on PK")
			require.Truef(t, isUniqueViolation(b.name, err),
				"expected UNIQUE/PK violation, got %T: %v", err, err)
		})
	}
}

// TestD1_7c_WatchdogState_FKCascadeOnInstanceDelete — deleting the
// parent sonarr_instance cascades the watchdog_state row.
func TestD1_7c_WatchdogState_FKCascadeOnInstanceDelete(t *testing.T) {
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

			_, err = db.ExecContext(ctx, insertWatchdogStateSQL(b.name),
				instance, int64(99), 2, 5, "")
			require.NoError(t, err)

			_, err = db.ExecContext(ctx,
				"DELETE FROM sonarr_instance WHERE name = "+placeholder(b.name, 1),
				instance)
			require.NoError(t, err)

			var n int
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM watchdog_state WHERE instance_name = "+placeholder(b.name, 1),
				instance).Scan(&n))
			require.Equal(t, 0, n, "watchdog_state row should CASCADE on instance DELETE")
		})
	}
}

// TestD1_7c_WatchdogBlacklist_FKCascadeOnInstanceDelete — same shape
// for blacklist.
func TestD1_7c_WatchdogBlacklist_FKCascadeOnInstanceDelete(t *testing.T) {
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

			_, err = db.ExecContext(ctx, insertWatchdogBlacklistSQL(b.name),
				instance, int64(99), 2, "rel", "reason", 0)
			require.NoError(t, err)

			_, err = db.ExecContext(ctx,
				"DELETE FROM sonarr_instance WHERE name = "+placeholder(b.name, 1),
				instance)
			require.NoError(t, err)

			var n int
			require.NoError(t, db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM watchdog_blacklist WHERE instance_name = "+placeholder(b.name, 1),
				instance).Scan(&n))
			require.Equal(t, 0, n, "watchdog_blacklist row should CASCADE on instance DELETE")
		})
	}
}

// TestD1_7c_WatchdogState_NullableColumns — INSERT with cooldown_until
// + last_error NULL; round-trip read confirms NULLs preserved.
func TestD1_7c_WatchdogState_NullableColumns(t *testing.T) {
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

			_, err = db.ExecContext(ctx, insertWatchdogStateNullableSQL(b.name),
				instance, int64(7), 0, 0)
			require.NoError(t, err)

			var cooldown sql.NullTime
			var lastErr sql.NullString
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT cooldown_until, last_error FROM watchdog_state WHERE instance_name = `+
					placeholder(b.name, 1)+` AND sonarr_series_id = `+placeholder(b.name, 2)+
					` AND season_number = `+placeholder(b.name, 3),
				instance, int64(7), 0).Scan(&cooldown, &lastErr))
			require.False(t, cooldown.Valid, "cooldown_until should be NULL")
			require.False(t, lastErr.Valid, "last_error should be NULL")
		})
	}
}

// TestD1_7c_WatchdogMigrationDown — apply 000001..000016, then
// m.Steps(-4) to roll back 000016 (app_config, added by 466b), 000015
// (scan_runs, added by 465b), 000014 (people_enrichment, added by
// 464b), and 000013 (watchdog); SELECT 1 from the watchdog tables
// errors (tables gone); Up() reapplies cleanly.
// The -4 step count covers the three migrations on top of 000013 added
// during the D-3 (people_enrichment), D-4 (scan_runs), and D-5
// (app_config) cutovers.
func TestD1_7c_WatchdogMigrationDown(t *testing.T) {
	for _, b := range allD1Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)
			require.NoError(t, m.Up())

			require.NoError(t, m.Steps(-4), "Steps(-4) should reverse 000016 + 000015 + 000014 + 000013")
			for _, tbl := range []string{"watchdog_state", "watchdog_blacklist"} {
				_, err := db.ExecContext(ctx, "SELECT 1 FROM "+tbl+" LIMIT 1")
				require.Errorf(t, err, "%s should be dropped after Down(4)", tbl)
			}

			require.NoError(t, m.Up(), "Up() should reapply 000013 + 000014 + 000015 + 000016 cleanly")
			for _, tbl := range []string{"watchdog_state", "watchdog_blacklist"} {
				_, err := db.ExecContext(ctx, "SELECT 1 FROM "+tbl+" LIMIT 1")
				require.NoErrorf(t, err, "%s should exist after re-Up", tbl)
			}
		})
	}
}

// ---------- SQL builders / helpers ----------

func insertWatchdogStateSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO watchdog_state
		        (instance_name, sonarr_series_id, season_number, attempt_count,
		         last_attempt_at, last_error, updated_at)
		        VALUES ($1, $2, $3, $4, now(), $5, now())`
	case "sqlite":
		return `INSERT INTO watchdog_state
		        (instance_name, sonarr_series_id, season_number, attempt_count,
		         last_attempt_at, last_error, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// insertWatchdogStateNullableSQL — INSERT without cooldown_until or
// last_error to test NULL round-trip.
func insertWatchdogStateNullableSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO watchdog_state
		        (instance_name, sonarr_series_id, season_number, attempt_count,
		         last_attempt_at, updated_at)
		        VALUES ($1, $2, $3, $4, now(), now())`
	case "sqlite":
		return `INSERT INTO watchdog_state
		        (instance_name, sonarr_series_id, season_number, attempt_count,
		         last_attempt_at, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

func insertWatchdogBlacklistSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO watchdog_blacklist
		        (instance_name, sonarr_series_id, season_number,
		         release_title, reason, consecutive,
		         blacklisted_at)
		        VALUES ($1, $2, $3, $4, $5, $6, now())`
	case "sqlite":
		return `INSERT INTO watchdog_blacklist
		        (instance_name, sonarr_series_id, season_number,
		         release_title, reason, consecutive,
		         blacklisted_at)
		        VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
