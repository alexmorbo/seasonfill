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

func TestFromEnv_MediaStoreDefaultsOff(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "off", cfg.MediaStore.Mode)
	assert.Equal(t, "seasonfill-media", cfg.MediaStore.S3.Bucket)
	assert.Equal(t, "us-east-1", cfg.MediaStore.S3.Region)
	assert.True(t, cfg.MediaStore.S3.UseSSL)
	assert.Equal(t, "/data/media", cfg.MediaStore.FSPath)
}

func TestFromEnv_MediaStoreS3RequiresCreds(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_MEDIA_STORE_MODE", "s3")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrMediaStoreS3Missing)
}

func TestFromEnv_MediaStoreS3Valid(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_MEDIA_STORE_MODE", "s3")
	t.Setenv("SEASONFILL_S3_ENDPOINT", "https://s3.morbo.dev")
	t.Setenv("SEASONFILL_S3_BUCKET", "seasonfill-media")
	t.Setenv("SEASONFILL_S3_ACCESS_KEY", "ak")
	t.Setenv("SEASONFILL_S3_SECRET_KEY", "sk")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "s3", cfg.MediaStore.Mode)
	assert.Equal(t, "https://s3.morbo.dev", cfg.MediaStore.S3.Endpoint)
}

func TestFromEnv_MediaStoreFSRequiresPath(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_MEDIA_STORE_MODE", "fs")
	t.Setenv("SEASONFILL_MEDIA_FS_PATH", "")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrMediaStoreFSMissing)
}

func TestFromEnv_MediaStoreUnknownMode(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_MEDIA_STORE_MODE", "weird")
	_, err := FromEnv()
	require.ErrorIs(t, err, ErrMediaStoreMode)
}
