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

// Story 346 — legacy SEASONFILL_TMDB_RPS still wins when the new
// SEASONFILL_TMDB_API_RPS env is unset. Backward-compat invariant for
// existing prod deployments.
func TestFromEnv_LegacyTMDBRPSFallback(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_RPS", "42")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 42.0, cfg.ExternalServices.TMDBAPIRPS, 0.001,
		"legacy SEASONFILL_TMDB_RPS must populate TMDBAPIRPS when the new env is unset")
	assert.InDelta(t, 42.0, cfg.ExternalServices.TMDBRPS, 0.001,
		"legacy TMDBRPS field still reads the legacy env (deprecated path)")
}

// Story 346 — when both envs are set, the new SEASONFILL_TMDB_API_RPS wins.
func TestFromEnv_NewEnvWinsOverLegacy(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_RPS", "10")
	t.Setenv("SEASONFILL_TMDB_API_RPS", "75")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 75.0, cfg.ExternalServices.TMDBAPIRPS, 0.001)
}

// Story 346 — SEASONFILL_TMDB_CDN_RPS plumbs into TMDBCDNRPS. 0 (unset)
// passes through so the downloader picks its own default.
func TestFromEnv_TMDBCDNRPS(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_CDN_RPS", "200")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 200.0, cfg.ExternalServices.TMDBCDNRPS, 0.001)
}

func TestFromEnv_TMDBCDNRPS_UnsetIsZero(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.0, cfg.ExternalServices.TMDBCDNRPS, 0.001,
		"unset TMDBCDNRPS must be 0 so downstream applies the package default")
}

// Story 347 — SEASONFILL_MEDIA_UNIFIED_RESOLVE default-on + kill-switch.

func TestFromEnv_MediaUnifiedResolve_DefaultsTrue(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	// SEASONFILL_MEDIA_UNIFIED_RESOLVE deliberately unset.
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.Enrichment.MediaUnifiedResolve,
		"unset SEASONFILL_MEDIA_UNIFIED_RESOLVE must default to true (story 347)")
}

func TestFromEnv_MediaUnifiedResolve_KillSwitchFalse(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "off", "FALSE"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
			t.Setenv("SEASONFILL_MEDIA_UNIFIED_RESOLVE", v)
			cfg, err := FromEnv()
			require.NoError(t, err)
			assert.False(t, cfg.Enrichment.MediaUnifiedResolve,
				"kill-switch %q must flip MediaUnifiedResolve off", v)
		})
	}
}

func TestFromEnv_MediaUnifiedResolve_ExplicitTrue(t *testing.T) {
	for _, v := range []string{"true", "1", "yes", "on", "TRUE"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
			t.Setenv("SEASONFILL_MEDIA_UNIFIED_RESOLVE", v)
			cfg, err := FromEnv()
			require.NoError(t, err)
			assert.True(t, cfg.Enrichment.MediaUnifiedResolve)
		})
	}
}

func TestFromEnv_MediaUnifiedResolve_GarbageFallsBackToDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_MEDIA_UNIFIED_RESOLVE", "banana")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.Enrichment.MediaUnifiedResolve,
		"unparseable env must fall back to default-on")
}
