package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/config"
)

func TestOpen_SQLite_InMemory(t *testing.T) {
	t.Parallel()
	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NotNil(t, db)
	assert.NoError(t, Ping(context.Background(), db))
}

func TestOpen_SQLite_FileInTempDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "test.db")

	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: path},
	})
	require.NoError(t, err)
	require.NotNil(t, db)
	assert.NoError(t, Ping(context.Background(), db))
	assert.FileExists(t, path, "sqlite file must be created")
}

func TestOpen_SQLite_EmptyPath(t *testing.T) {
	t.Parallel()
	_, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ""},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite path is empty")
}

func TestOpen_Postgres_EmptyDSN(t *testing.T) {
	t.Parallel()
	_, err := Open(config.DatabaseConfig{
		Driver:   "postgres",
		Postgres: config.PostgresConfig{DSN: ""},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres dsn is empty")
}

func TestOpen_Postgres_BadDSN(t *testing.T) {
	t.Parallel()
	// A syntactically invalid DSN — Open returns error before any network IO.
	_, err := Open(config.DatabaseConfig{
		Driver: "postgres",
		Postgres: config.PostgresConfig{
			DSN:             "this-is-not-a-valid-dsn",
			MaxOpenConns:    1,
			MaxIdleConns:    1,
			ConnMaxLifetime: time.Second,
		},
	})
	require.Error(t, err)
}

func TestOpen_UnknownDriver(t *testing.T) {
	t.Parallel()
	_, err := Open(config.DatabaseConfig{Driver: "mongodb"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownDriver))
}

func TestPing_OK(t *testing.T) {
	t.Parallel()
	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	assert.NoError(t, Ping(context.Background(), db))
}

func TestOpen_SQLite_NowFuncIsInvoked(t *testing.T) {
	t.Parallel()
	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)
	require.NoError(t, Migrate(db))
	// Insert a record — GORM calls NowFunc to populate CreatedAt/UpdatedAt.
	row := ScanRunModel{
		ID:     NewScanID(),
		Status: "running",
	}
	require.NoError(t, db.Create(&row).Error)
}

func TestOpen_SQLite_MkdirError(t *testing.T) {
	t.Parallel()
	// Create a regular file, then use it as the parent directory — MkdirAll will fail.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "notadir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))

	_, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: filepath.Join(blocker, "child", "test.db")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mkdir")
}

func TestOpen_SQLite_OpenError(t *testing.T) {
	t.Parallel()
	// Using an existing directory as the file path causes sqlite to fail to open.
	dir := t.TempDir()
	_, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: dir},
	})
	require.Error(t, err)
}

func TestPing_Error(t *testing.T) {
	t.Parallel()
	db, err := Open(config.DatabaseConfig{
		Driver: "sqlite",
		SQLite: config.SQLiteConfig{Path: ":memory:"},
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	assert.Error(t, Ping(context.Background(), db))
}
