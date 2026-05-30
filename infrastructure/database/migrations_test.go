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
	assert.Equal(t, 1, version)
	assert.False(t, dirty)
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
	assert.Equal(t, 1, version)
	assert.False(t, dirty)

	assert.True(t, db.Migrator().HasTable("scan_runs"))
	assert.True(t, db.Migrator().HasColumn("sonarr_instance", "ranking_origin_bonus"))
	assert.True(t, db.Migrator().HasIndex("grab_records", "idx_grab_dedupe"))
}
