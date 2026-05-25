package database

import (
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

	assert.True(t, db.Migrator().HasColumn(&RuntimeConfigModel{}, "security_allow_private_targets"),
		"AutoMigrate must create the security_allow_private_targets column")

	// Idempotent — running twice must not error.
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
	assert.Contains(t, err.Error(), "auto-migrate")
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
