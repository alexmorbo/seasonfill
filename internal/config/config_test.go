package config

import (
	"testing"
	"time"

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

// W110-2 — SEASONFILL_SKELETON_COLD_MEDIA_SEED default-on + kill-switch.
func TestFromEnv_SkeletonColdMediaSeed_DefaultsTrue(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	// SEASONFILL_SKELETON_COLD_MEDIA_SEED deliberately unset.
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.True(t, cfg.Enrichment.SkeletonColdMediaSeed,
		"unset SEASONFILL_SKELETON_COLD_MEDIA_SEED must default to true (W110-2)")
}

func TestFromEnv_SkeletonColdMediaSeed_KillSwitchFalse(t *testing.T) {
	for _, v := range []string{"false", "0", "no", "off"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
			t.Setenv("SEASONFILL_SKELETON_COLD_MEDIA_SEED", v)
			cfg, err := FromEnv()
			require.NoError(t, err)
			assert.False(t, cfg.Enrichment.SkeletonColdMediaSeed,
				"kill-switch %q must flip SkeletonColdMediaSeed off", v)
		})
	}
}

func TestFromEnv_WebhookBaseURL_Set(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_WEBHOOK_BASE_URL", "  https://sf.example/  ")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "https://sf.example", cfg.HTTP.WebhookBaseURL,
		"whitespace and trailing slash must be normalized away")
}

func TestFromEnv_WebhookBaseURL_UnsetEmpty(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.HTTP.WebhookBaseURL)
}

// Story 1096 — enrichment worker/concurrency knobs.
func TestFromEnv_EnrichmentWorkerDefaults(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.Enrichment.EnrichmentSeriesWorkers)
	assert.Equal(t, 1, cfg.Enrichment.EnrichmentPersonWorkers)
	assert.Equal(t, 4, cfg.Enrichment.EnrichmentSeasonConcurrency)
}

func TestFromEnv_EnrichmentWorkerParsed(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_ENRICHMENT_SERIES_WORKERS", "6")
	t.Setenv("SEASONFILL_ENRICHMENT_PERSON_WORKERS", "3")
	t.Setenv("SEASONFILL_ENRICHMENT_SEASON_CONCURRENCY", "8")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 6, cfg.Enrichment.EnrichmentSeriesWorkers)
	assert.Equal(t, 3, cfg.Enrichment.EnrichmentPersonWorkers)
	assert.Equal(t, 8, cfg.Enrichment.EnrichmentSeasonConcurrency)
}

// getenvInt floors 0/negative/unparseable to the default, so a bad env can
// never disable a worker (all defaults are >=1).
func TestFromEnv_EnrichmentWorkerZeroFallsBackToDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_ENRICHMENT_SERIES_WORKERS", "0")
	t.Setenv("SEASONFILL_ENRICHMENT_PERSON_WORKERS", "-4")
	t.Setenv("SEASONFILL_ENRICHMENT_SEASON_CONCURRENCY", "notanint")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 2, cfg.Enrichment.EnrichmentSeriesWorkers)
	assert.Equal(t, 1, cfg.Enrichment.EnrichmentPersonWorkers)
	assert.Equal(t, 4, cfg.Enrichment.EnrichmentSeasonConcurrency)
	assert.GreaterOrEqual(t, cfg.Enrichment.EnrichmentSeriesWorkers, 1)
	assert.GreaterOrEqual(t, cfg.Enrichment.EnrichmentPersonWorkers, 1)
	assert.GreaterOrEqual(t, cfg.Enrichment.EnrichmentSeasonConcurrency, 1)
}

// Story 1097 — background refresh drain levers (batch_size + interval).
func TestFromEnv_EnrichmentRefreshDefaults(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 50, cfg.Enrichment.EnrichmentRefreshBatchSize)
	assert.Equal(t, 30*time.Minute, cfg.Enrichment.EnrichmentRefreshInterval)
}

func TestFromEnv_EnrichmentRefreshParsed(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_BATCH_SIZE", "150")
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS", "600")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 150, cfg.Enrichment.EnrichmentRefreshBatchSize)
	assert.Equal(t, 10*time.Minute, cfg.Enrichment.EnrichmentRefreshInterval)
}

// getenvInt floors 0/negative/unparseable to the batch default (50, >=1),
// so a bad batch env can never stall the sweep.
func TestFromEnv_EnrichmentRefreshBatchZeroFallsBackToDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_BATCH_SIZE", "0")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 50, cfg.Enrichment.EnrichmentRefreshBatchSize)
	assert.GreaterOrEqual(t, cfg.Enrichment.EnrichmentRefreshBatchSize, 1)

	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_BATCH_SIZE", "notanint")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 50, cfg.Enrichment.EnrichmentRefreshBatchSize)
}

// The interval env is integer SECONDS, floored at 60s; 0 / unset /
// unparseable → the 30m default.
func TestFromEnv_EnrichmentRefreshIntervalFloorAndDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")

	// Below the 60s floor collapses UP to 60s.
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS", "10")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, cfg.Enrichment.EnrichmentRefreshInterval)

	// Zero / negative → 30m default.
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS", "0")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, cfg.Enrichment.EnrichmentRefreshInterval)

	// Garbage → 30m default.
	t.Setenv("SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS", "notanint")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 30*time.Minute, cfg.Enrichment.EnrichmentRefreshInterval)
}

func TestFromEnv_MediaServeGetBudgetDefaults(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, cfg.ExternalServices.MediaServeGetBudget)
	assert.Equal(t, 24, cfg.ExternalServices.MediaS3ReadInflight)
	assert.Equal(t, 12, cfg.ExternalServices.MediaS3WriteInflight)
}

func TestFromEnv_MediaServeGetBudgetParsedAndFloor(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")

	// Explicit ms value.
	t.Setenv("SEASONFILL_MEDIA_SERVE_GET_BUDGET", "3000")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 3000*time.Millisecond, cfg.ExternalServices.MediaServeGetBudget)

	// Below the 100ms floor collapses UP to 100ms.
	t.Setenv("SEASONFILL_MEDIA_SERVE_GET_BUDGET", "10")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 100*time.Millisecond, cfg.ExternalServices.MediaServeGetBudget)

	// Zero → default.
	t.Setenv("SEASONFILL_MEDIA_SERVE_GET_BUDGET", "0")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, cfg.ExternalServices.MediaServeGetBudget)

	// Garbage → default.
	t.Setenv("SEASONFILL_MEDIA_SERVE_GET_BUDGET", "notanint")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 1500*time.Millisecond, cfg.ExternalServices.MediaServeGetBudget)
}

func TestFromEnv_MediaS3InflightParsedAndDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")

	t.Setenv("SEASONFILL_MEDIA_S3_READ_INFLIGHT", "40")
	t.Setenv("SEASONFILL_MEDIA_S3_WRITE_INFLIGHT", "8")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 40, cfg.ExternalServices.MediaS3ReadInflight)
	assert.Equal(t, 8, cfg.ExternalServices.MediaS3WriteInflight)

	// Negative / unparseable → defaults (getenvInt floors <=0 to def).
	t.Setenv("SEASONFILL_MEDIA_S3_READ_INFLIGHT", "-5")
	t.Setenv("SEASONFILL_MEDIA_S3_WRITE_INFLIGHT", "notanint")
	cfg, err = FromEnv()
	require.NoError(t, err)
	assert.Equal(t, 24, cfg.ExternalServices.MediaS3ReadInflight)
	assert.Equal(t, 12, cfg.ExternalServices.MediaS3WriteInflight)
}

// W110-5 (F-03) — SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC parsing + clamp.
func TestFromEnv_InteractiveReserveFrac_DefaultWhenUnset(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.25, cfg.ExternalServices.TMDBInteractiveReserveFrac, 0.0001,
		"unset env must fall back to the 0.25 default")
}

func TestFromEnv_InteractiveReserveFrac_Passthrough(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC", "0.3")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.3, cfg.ExternalServices.TMDBInteractiveReserveFrac, 0.0001,
		"in-band value must pass through unchanged")
}

func TestFromEnv_InteractiveReserveFrac_ClampFloor(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC", "0.01")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.05, cfg.ExternalServices.TMDBInteractiveReserveFrac, 0.0001,
		"sub-floor value must clamp up to 0.05")
}

func TestFromEnv_InteractiveReserveFrac_ClampCeil(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC", "0.9")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.5, cfg.ExternalServices.TMDBInteractiveReserveFrac, 0.0001,
		"above-ceil value must clamp down to 0.5")
}

func TestFromEnv_InteractiveReserveFrac_NegativeToDefault(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC", "-1")
	cfg, err := FromEnv()
	require.NoError(t, err)
	assert.InDelta(t, 0.25, cfg.ExternalServices.TMDBInteractiveReserveFrac, 0.0001,
		"negative value must fall back to the default")
}
