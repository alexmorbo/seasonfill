package gc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/config"
)

func newSQLiteDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	return db
}

func TestEventPrune_MissingTable_Skips(t *testing.T) {
	t.Parallel()
	db := newSQLiteDB(t)
	build := EventPruneDeps{DB: db}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.True(t, res.Skipped)
	assert.Equal(t, "table_not_present_pending_a3", res.SkipReason)
	assert.Equal(t, 0, res.Deleted)
}

func TestEventPrune_TablePresent_DeletesOldRows(t *testing.T) {
	t.Parallel()
	db := newSQLiteDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE qbit_torrent_events (id INTEGER PRIMARY KEY, created_at datetime)`).Error)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := now.Add(-200 * 24 * time.Hour)
	fresh := now.Add(-10 * 24 * time.Hour)
	require.NoError(t, db.Exec(`INSERT INTO qbit_torrent_events (id, created_at) VALUES (1, ?), (2, ?)`, old, fresh).Error)

	build := EventPruneDeps{
		DB:    db,
		Clock: func() time.Time { return now },
	}.Build()
	res, err := build(context.Background())
	require.NoError(t, err)
	assert.False(t, res.Skipped)
	assert.Equal(t, 1, res.Deleted)
}

func TestTableExists_NilDB(t *testing.T) {
	t.Parallel()
	assert.False(t, tableExists(context.Background(), nil, "x"))
}
