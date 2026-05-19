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
