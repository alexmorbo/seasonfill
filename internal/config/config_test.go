package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromEnv_SqliteDefaults(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "sqlite", cfg.Database.Driver)
	assert.Equal(t, "./data/seasonfill.db", cfg.Database.SQLite.Path)
	assert.Equal(t, ":8080", cfg.HTTP.Bind)
	assert.Equal(t, "info", cfg.Log.Level)
}

func TestFromEnv_PostgresRequiresDSN(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "postgres")
	t.Setenv("SEASONFILL_DATABASE_POSTGRES_DSN", "")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrPostgresDSN)
}

func TestFromEnv_PasswordMutex(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "plain")
	t.Setenv("SEASONFILL_WEB_PASSWORD_HASH", "$2a$12$abc")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrPasswordMutex)
}

func TestFromEnv_UnknownDriver(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "weird")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrUnknownDriver)
}

func TestFromEnv_PostgresValid(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "postgres")
	t.Setenv("SEASONFILL_DATABASE_POSTGRES_DSN", "postgres://u:p@h/db")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "postgres", cfg.Database.Driver)
	assert.Equal(t, "postgres://u:p@h/db", cfg.Database.Postgres.DSN)
}

func TestFromEnv_WebUserDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "admin", cfg.Auth.WebUser)
}
