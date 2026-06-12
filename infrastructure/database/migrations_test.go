package database

import (
	"context"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/config"
)

func TestMigrate_CreatesTables(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasTable(&ScanRunModel{}))
	assert.True(t, db.Migrator().HasTable(&DecisionModel{}))
	assert.True(t, db.Migrator().HasTable(&GrabRecordModel{}))
	assert.True(t, db.Migrator().HasTable(&OriginReleaseModel{}))
	assert.True(t, db.Migrator().HasTable(&CooldownModel{}))
	assert.True(t, db.Migrator().HasTable(&RuntimeConfigModel{}))
	assert.True(t, db.Migrator().HasTable(&SonarrInstanceModel{}))
	assert.True(t, db.Migrator().HasTable(&InstanceSecretModel{}))
	// 039a: Phase 10 Watchdog foundation.
	assert.True(t, db.Migrator().HasTable(&InstanceQbitSettingsModel{}))
	assert.True(t, db.Migrator().HasTable(&WatchdogBlacklistModel{}))
	assert.True(t, db.Migrator().HasColumn(&GrabRecordModel{}, "torrent_hash"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_records_torrent_hash"))
	assert.True(t, db.Migrator().HasIndex("instance_qbit_settings", "idx_instance_qbit_settings_instance_id"))
	assert.True(t, db.Migrator().HasIndex("watchdog_blacklist", "idx_watchdog_blacklist_triple"))
	// 039f-1: regrab counter table + grab_records.replay_of_id.
	assert.True(t, db.Migrator().HasColumn(&GrabRecordModel{}, "replay_of_id"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_records_replay_of_id"))
	assert.True(t, db.Migrator().HasTable("regrab_no_better_counter"))
	assert.True(t, db.Migrator().HasIndex("regrab_no_better_counter", "idx_regrab_no_better_counter_triple"))
	// 082: F-P2-1 backend — qbit_public_url column on instance_qbit_settings.
	assert.True(t, db.Migrator().HasColumn(&InstanceQbitSettingsModel{}, "qbit_public_url"))

	// Idempotent — running twice must be a no-op.
	assert.NoError(t, Migrate(db))
}

func TestMigrate_Error(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = Migrate(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database is closed")
}

func TestMigrate_CreatesAuditCompositeIndexes(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	cases := []struct {
		table string
		index string
	}{
		{"scan_runs", "idx_scan_runs_created_at_id"},
		{"decisions", "idx_decisions_created_at_id"},
		{"grab_records", "idx_grab_records_created_at_id"},
	}
	for _, tc := range cases {
		t.Run(tc.index, func(t *testing.T) {
			assert.True(t, db.Migrator().HasIndex(tc.table, tc.index),
				"expected %s on %s", tc.index, tc.table)
		})
	}
}

// TestMigrate_StampsBaselineOnExistingDB simulates a pre-existing prod DB
// (app tables present, no schema_migrations) and asserts the stamp path
// leaves us at version=1 dirty=false without touching the application
// schema.
func TestMigrate_StampsBaselineOnExistingDB(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	require.NoError(t, Migrate(db))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	ctx := context.Background()
	_, err = sqlDB.ExecContext(ctx, `DROP TABLE schema_migrations`)
	require.NoError(t, err)

	require.NoError(t, Migrate(db))

	var version int
	var dirty bool
	require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty))
	// 203 (B-1a): latest migration is 000026_entity_core.
	assert.Equal(t, 26, version)
	assert.False(t, dirty)
}

// TestMigrate_UpgradesToV2 asserts that applying the migration chain
// (v1 baseline → v2 auth_modes) leaves the runtime_config row shape
// extended with the 4 new auth columns + the expected defaults.
func TestMigrate_UpgradesToV2(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "auth_mode"))
	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "auth_local_bypass"))
	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "auth_local_networks"))
	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "auth_session_epoch"))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	ctx := context.Background()
	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO runtime_config (id, cron_enabled, cron_schedule) VALUES (1, 1, 'x')`)
	require.NoError(t, err)
	var mode string
	var bypass int
	var networks string
	var epoch int64
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT auth_mode, auth_local_bypass, auth_local_networks, auth_session_epoch FROM runtime_config WHERE id=1`).
		Scan(&mode, &bypass, &networks, &epoch))
	assert.Equal(t, "forms", mode)
	assert.Equal(t, 0, bypass)
	assert.Contains(t, networks, "127.0.0.0/8")
	assert.Equal(t, int64(0), epoch)
}

// TestMigrationFilesEmbedded asserts the embed.FS is wired correctly.
func TestMigrationFilesEmbedded(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"postgres", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			t.Parallel()
			dir := "migrations/" + dialect
			data, err := migrationsFS.ReadFile(dir + "/000001_baseline.up.sql")
			require.NoError(t, err)
			assert.Contains(t, string(data), "CREATE TABLE",
				"baseline up.sql should contain CREATE TABLE statements")
			assert.Contains(t, string(data), "scan_runs",
				"baseline up.sql should mention scan_runs")
		})
	}
}

// TestMigrationFilesHaveDownSibling enforces the convention that every
// up.sql ships with a paired down.sql so we never accidentally land a
// migration with no rollback path.
func TestMigrationFilesHaveDownSibling(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{"postgres", "sqlite"} {
		dir := "migrations/" + dialect
		entries, err := fs.ReadDir(migrationsFS, dir)
		require.NoError(t, err)

		ups := map[string]bool{}
		downs := map[string]bool{}
		for _, e := range entries {
			name := e.Name()
			switch {
			case strings.HasSuffix(name, ".up.sql"):
				ups[strings.TrimSuffix(name, ".up.sql")] = true
			case strings.HasSuffix(name, ".down.sql"):
				downs[strings.TrimSuffix(name, ".down.sql")] = true
			}
		}
		for prefix := range ups {
			assert.True(t, downs[prefix], "missing %s", path.Join(dir, prefix+".down.sql"))
		}
		for prefix := range downs {
			assert.True(t, ups[prefix], "missing %s", path.Join(dir, prefix+".up.sql"))
		}
	}
}

// TestMigrate_V21_AddsIntentColumn asserts the 091a / F-P2-2 migration
// adds the `intent` column to the decisions table on SQLite. The
// Postgres mirror is exercised in CI via the Postgres integration
// test below.
func TestMigrate_V21_AddsIntentColumn(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasColumn(&DecisionModel{}, "intent"),
		"v21 must add the intent column to decisions")
}

// TestMigrate_V23_WidensCooldownReason asserts the 118 migration
// keeps the `reason` column on `cooldowns` readable as text-typed
// storage. On SQLite the baseline already stored TEXT, so this test
// is a smoke check that the migration applied without error and the
// column is still writable. The dialect-specific type assertion runs
// in TestMigrate_PostgresIntegration below.
func TestMigrate_V23_WidensCooldownReason(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasColumn(&CooldownModel{}, "reason"),
		"v23 must keep the reason column on cooldowns")

	sqlDB, err := db.DB()
	require.NoError(t, err)
	ctx := context.Background()
	bigReason := strings.Repeat("z", 2048)
	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO cooldowns (scope, key, expires_at, reason, created_at)
		 VALUES ('guid', 'k', datetime('now','+1 hour'), ?, datetime('now'))`,
		bigReason)
	require.NoError(t, err, "v23 must allow >128-byte reason writes")
	var got string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT reason FROM cooldowns WHERE scope='guid' AND key='k'`).Scan(&got))
	assert.Equal(t, bigReason, got, "sqlite has no length affinity — full body persists")
}

// TestMigrate_V22_AddsGUIDRewritesColumn asserts the 107 migration adds
// the `guid_rewrites` column to runtime_config and that the default
// value is the literal JSON `[]`.
func TestMigrate_V22_AddsGUIDRewritesColumn(t *testing.T) {
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))

	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "guid_rewrites"),
		"v22 must add the guid_rewrites column to runtime_config")

	sqlDB, err := db.DB()
	require.NoError(t, err)
	ctx := context.Background()
	_, err = sqlDB.ExecContext(ctx,
		`INSERT INTO runtime_config (id, cron_enabled, cron_schedule) VALUES (1, 1, 'x')`)
	require.NoError(t, err)
	var rewrites string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT guid_rewrites FROM runtime_config WHERE id=1`).Scan(&rewrites))
	assert.Equal(t, "[]", rewrites,
		"default must be the literal JSON empty array")
}

// TestMigrate_PostgresIntegration runs the full migrate path against a
// real Postgres. Skipped unless SEASONFILL_TEST_POSTGRES_DSN is set so CI
// can opt in without forcing every developer to bring up a database.
func TestMigrate_PostgresIntegration(t *testing.T) {
	dsn := os.Getenv("SEASONFILL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set SEASONFILL_TEST_POSTGRES_DSN to run")
	}
	t.Parallel()

	db, err := Open(config.DatabaseConfig{
		Driver:   "postgres",
		Postgres: config.PostgresConfig{DSN: dsn},
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	ctx := context.Background()
	for _, table := range []string{
		"regrab_no_better_counter",
		"watchdog_blacklist", "instance_qbit_settings",
		"instance_secret", "sonarr_instance", "runtime_config",
		"admin_users", "cooldowns", "origin_releases",
		"grab_records", "decisions", "scan_runs", "schema_migrations",
	} {
		_, _ = sqlDB.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE")
	}

	require.NoError(t, Migrate(db))

	var version int
	var dirty bool
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty))
	// 203 (B-1a): latest migration is 000026_entity_core.
	assert.Equal(t, 26, version)
	assert.False(t, dirty)

	assert.True(t, db.Migrator().HasTable("scan_runs"))
	assert.True(t, db.Migrator().HasColumn("sonarr_instance", "ranking_origin_bonus"))
	assert.False(t, db.Migrator().HasIndex("grab_records", "idx_grab_dedupe"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_dedupe_lookup"))
	// 039a: Phase 10 Watchdog foundation.
	assert.True(t, db.Migrator().HasTable("instance_qbit_settings"))
	assert.True(t, db.Migrator().HasTable("watchdog_blacklist"))
	assert.True(t, db.Migrator().HasColumn("grab_records", "torrent_hash"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_records_torrent_hash"))
	// 039f-1: regrab counter + grab_records.replay_of_id.
	assert.True(t, db.Migrator().HasColumn("grab_records", "replay_of_id"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_records_replay_of_id"))
	assert.True(t, db.Migrator().HasTable("regrab_no_better_counter"))
	assert.True(t, db.Migrator().HasIndex("regrab_no_better_counter", "idx_regrab_no_better_counter_triple"))

	// 118: cooldowns.reason must be `text` after migration 23.
	var dataType string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT data_type FROM information_schema.columns
		   WHERE table_name='cooldowns' AND column_name='reason'`).Scan(&dataType))
	assert.Equal(t, "text", dataType,
		"v23 must widen cooldowns.reason to text on Postgres")
}
