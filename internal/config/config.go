package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// Compatibility type aliases — runtime snapshots are the source of truth;
// downstream packages still reference config.X for backward-compat.
type (
	SonarrInstance  = runtime.InstanceSnapshot
	SearchConfig    = runtime.SearchSnapshot
	RankingConfig   = runtime.RankingSnapshot
	LimitsConfig    = runtime.LimitsSnapshot
	CooldownConfig  = runtime.CooldownSnapshot
	RetryConfig     = runtime.RetrySnapshot
	RateLimitConfig = runtime.RateLimitSnapshot
	TagsConfig      = runtime.TagsSnapshot
	CronConfig      = runtime.CronSnapshot
	ScanConfig      = runtime.ScanSnapshot
	AuthConfig      = Auth
)

// HealthCheckConfig is a compatibility wrapper because the field names
// changed: old code uses RecheckInterval{Auth,Network}; new runtime
// uses Recheck{Auth,Network}.
type HealthCheckConfig struct {
	RecheckIntervalAuth    time.Duration
	RecheckIntervalNetwork time.Duration
}

// NewHealthCheckConfig converts runtime.HealthCheckSnapshot to HealthCheckConfig.
func NewHealthCheckConfig(hc runtime.HealthCheckSnapshot) HealthCheckConfig {
	return HealthCheckConfig{
		RecheckIntervalAuth:    hc.RecheckAuth,
		RecheckIntervalNetwork: hc.RecheckNetwork,
	}
}

// Auth config is now split between bootstrap (APIKey, WebUser, WebPassword,
// WebPasswordHash) and runtime (SessionTTL, SecureCookie, TrustedProxies).
// For compatibility with existing HTTP handler code, we define a type that
// bridges both sources.
type Auth struct {
	Enabled        bool
	APIKey         string
	SessionTTL     time.Duration
	SecureCookie   bool
	TrustedProxies []string
	// SessionEpoch is the app_config session-invalidation generation loaded
	// at boot. Threaded through so the edge server can seed the shared
	// AuthRuntime with the real epoch BEFORE the reload subscriber's first
	// apply — otherwise a pre-bump (epoch < live) cookie validates during the
	// boot window because VerifySession rejects only Epoch < currentEpoch and
	// 0 < 0 is false. Runtime-mutable: the reload subscriber overwrites the
	// live value from snap.Auth.SessionEpoch on every publish.
	SessionEpoch     int64
	OIDCClientSecret string
	// Below are bootstrap-only; kept on this struct for test-fixture
	// compatibility. Server runtime does not read them — see AuthBootstrap.
	WebUser         string
	WebPassword     string
	WebPasswordHash string
}

// Bootstrap holds the env-derived settings needed to reach the DB
// and bootstrap the admin user. EVERYTHING else (cron, scan tuning,
// dry_run, global rate limit, sonarr instances, runtime-mutable auth
// fields) lives in the DB and is loaded into internal/runtime.Snapshot
// at startup.
type Bootstrap struct {
	Log              LogConfig
	HTTP             HTTPConfig
	Database         DatabaseConfig
	Auth             AuthBootstrap
	MediaStore       MediaStoreConfig
	ExternalServices ExternalServicesEnv
	Enrichment       EnrichmentConfig
	Discovery        DiscoveryConfig
}

// DiscoveryConfig carries the env-only tuning knobs for the discovery
// bounded context. Story 568 A2 introduces PreWarmEnabled — the toggle
// for the per-refresh pre-warm fan-out that fills
// series_texts.{seriesID, activeLang} before the user clicks a
// discovery tile.
type DiscoveryConfig struct {
	// PreWarmEnabled gates the A2 pre-warm hook (Story 568). DEFAULT
	// ON; when true (the default), the discovery Worker fans out
	// RefreshSeriesText(force=false) per (item, activeLang) at the
	// end of every successful ReplaceList. When false the worker's
	// refresh() success branch skips the fan-out (config-toggle
	// rollback path). Env var: SEASONFILL_DISCOVERY_PREWARM_ENABLED.
	PreWarmEnabled bool
}

// EnrichmentConfig carries the env-only tuning knobs for the
// enrichment dispatcher. Story 318 ships the cold-start periodic
// re-sweep interval; future stories layer additional knobs here.
type EnrichmentConfig struct {
	// ColdStartResweepInterval governs how often the cold-start
	// backfill goroutine re-queries the canonical series table for
	// rows whose enrichment_tmdb_synced_at is NULL. Production
	// default is 60s — fast enough to clear a stranded backlog
	// within minutes after a queue-full drop, cheap enough that the
	// single PK-indexed LEFT JOIN does not move the DB needle.
	// Override via SEASONFILL_ENRICHMENT_COLDSTART_RESWEEP_SECONDS
	// (clamped to >=5s; 0 or unset → default).
	ColdStartResweepInterval time.Duration

	// MediaUnifiedResolve gates the story-347 always-emit-hash
	// contract. DEFAULT ON; the env var SEASONFILL_MEDIA_UNIFIED_RESOLVE
	// is a kill-switch — set "false"/"0"/"no"/"off" to revert to the
	// legacy nil-on-miss path with zero schema impact, unset / "true" /
	// "1" / "yes" / "on" keeps the unified contract active. When ON
	// (the default), MediaResolver.Resolve emits a real or sentinel
	// hash for every call so the frontend has a stable visual slot.
	MediaUnifiedResolve bool

	// SkeletonColdMediaSeed (W110-2) gates the synchronous cold poster-presence
	// seed in the skeleton composer. DEFAULT ON; env
	// SEASONFILL_SKELETON_COLD_MEDIA_SEED is a kill-switch — "false"/"0"/"no"/
	// "off" reverts to the legacy sentinel-on-cold + self-heal-on-refresh path.
	// When ON, the first open of a (series,lang) pair whose requested-lang
	// series_media_texts poster presence is unknown runs a forced SectionSkeleton
	// seed so the first paint carries a real/eager poster hash, not the sentinel.
	SkeletonColdMediaSeed bool

	// EnrichmentSeriesWorkers is the number of concurrent series-hydration
	// goroutines the dispatcher spawns. Story 1096 — env-tunable so the
	// operator can raise concurrency toward the 50 rps TMDB cap without a
	// rebuild. Default 2 (the pre-1096 hardcoded value). Env:
	// SEASONFILL_ENRICHMENT_SERIES_WORKERS. getenvInt floors 0/negative/
	// unparseable → default, so a bad env can never disable the worker.
	EnrichmentSeriesWorkers int

	// EnrichmentPersonWorkers is the number of concurrent person-hydration
	// goroutines. Story 1096 — default 1 (pre-1096 hardcoded value). Env:
	// SEASONFILL_ENRICHMENT_PERSON_WORKERS. Same 0→default floor as above.
	EnrichmentPersonWorkers int

	// EnrichmentSeasonConcurrency bounds the per-language parallel
	// GetSeason fan-out inside the series worker (Story 1096, Fix B).
	// Default 4 (modestly parallel). Env:
	// SEASONFILL_ENRICHMENT_SEASON_CONCURRENCY. Same 0→default floor.
	EnrichmentSeasonConcurrency int

	// EnrichmentRefreshBatchSize is the number of series the background
	// RefreshScheduler picks per tick (Story 1097). Together with the
	// tick interval it sets the backfill drain rate = batch × (1/interval).
	// Default 50 (the pre-1097 hardcoded value). Env:
	// SEASONFILL_ENRICHMENT_REFRESH_BATCH_SIZE. getenvInt floors 0/negative/
	// unparseable → default, so a bad env can never stall the sweep.
	EnrichmentRefreshBatchSize int

	// EnrichmentRefreshInterval is the cadence of the background
	// RefreshScheduler tick (Story 1097). Default 30m (the pre-1097
	// hardcoded value). Env: SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS
	// (integer seconds, floored at 60s; 0 / unset / unparseable → default).
	EnrichmentRefreshInterval time.Duration

	// Changes (W2-6) — Wave 2 TMDB /tv/changes poller knobs. DEFAULT
	// Enabled=false; see ChangesConfig. Nested here (not a sibling on
	// Bootstrap) to mirror how the enrichment context owns its own knobs.
	Changes ChangesConfig
}

// ChangesConfig carries the env-only knobs for the Wave 2 TMDB /tv/changes
// firehose poller (plan §10). DEFAULT Enabled=false — the poller ships inert
// (dark-launch; G4 zero-regression). server.go double-gates the loop on
// cfg.Cron.Enabled && Enrichment.Changes.Enabled.
type ChangesConfig struct {
	// Enabled — SEASONFILL_TMDB_CHANGES_ENABLED. Default false.
	Enabled bool
	// PollInterval — SEASONFILL_TMDB_CHANGES_POLL_INTERVAL (a Go duration
	// string, e.g. "8h"). Default 8h; min-clamped to 1h (anti-tick-storm).
	PollInterval time.Duration
	// OverlapDays — SEASONFILL_TMDB_CHANGES_OVERLAP_DAYS. Default 1; clamped
	// to [1..7]. (getenvInt floors <=0 to the default, so explicit 0 is not
	// expressible — out of scope; NewChangesPoller also floors <=0→1.)
	OverlapDays int
	// LookbackDays — SEASONFILL_TMDB_CHANGES_MAX_LOOKBACK_DAYS. Default 14 (=
	// API window cap); clamped to [1..14].
	LookbackDays int
	// PageCap — SEASONFILL_TMDB_CHANGES_PAGE_CAP. Default 200 (pagination
	// safety valve). getenvInt floors <=0 to the default; no upper clamp.
	PageCap int
	// MarkBatch — SEASONFILL_TMDB_CHANGES_MARK_BATCH. Default 500 (IN-chunk
	// size); clamped to [50..900].
	MarkBatch int
}

// ExternalServicesEnv carries the env-only overrides for the three
// enrichment sources (PRD §10.4.4: env > DB per field). All fields
// default to "" — empty means "use whatever the DB row supplies".
// The use case consumes these via an EnvLookup; production wires
// os.Getenv, tests inject a fixture.
type ExternalServicesEnv struct {
	TMDBToken     string
	TMDBProxyURL  string
	TMDBProxyUser string
	TMDBProxyPass string
	// Story 313 — TMDB self-cap target in requests-per-second. Float
	// (e.g. 50, 4.5). 0 or unset → infrastructure/tmdb default (50).
	// Operator drops this when prod 429s show up in
	// tmdb_rate_limit_pauses_total.
	//
	// Story 346: DEPRECATED. Kept as a legacy alias for
	// TMDBAPIRPS — when the new SEASONFILL_TMDB_API_RPS is unset,
	// SEASONFILL_TMDB_RPS still wins. main.go logs a one-shot
	// deprecation warning at boot when only the legacy name is set.
	// Remove next release.
	//
	// Deprecated: use TMDBAPIRPS instead.
	TMDBRPS float64
	// Story 346 — split rate limiters for the TMDB API host
	// (api.themoviedb.org) and the image CDN host (image.tmdb.org).
	// The two hosts have wildly different per-IP budgets; sharing one
	// limiter stalled the on-demand fetcher behind every API call.
	//
	// TMDBAPIRPS: API host self-cap. 0 or unset → infrastructure/tmdb
	// default (50). Operator-tunable for adaptive rate-limit pauses.
	// Env: SEASONFILL_TMDB_API_RPS. Legacy fallback honours
	// SEASONFILL_TMDB_RPS for one release.
	//
	// TMDBCDNRPS: image CDN cap consumed by the media downloader and
	// the on-demand fetcher. 0 or unset → UNCAPPED (rate.Inf) as of
	// W19-1; the CDN is Cloudflare-backed with no published per-IP
	// limit. A positive value re-imposes a finite cap (rollback).
	// Env: SEASONFILL_TMDB_CDN_RPS.
	TMDBAPIRPS float64
	TMDBCDNRPS float64
	// TMDBInteractiveReserveFrac — W110-5 (F-03). Fraction of the TMDB API rps
	// budget reserved exclusively for interactive (on-view freshener) callers;
	// batch/background callers are throttled to rps×(1−frac). Env:
	// SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC. unset/<=0 → default 0.25;
	// clamped to [0.05, 0.5]. Mirrors tmdb.ClampInteractiveReserveFrac (kept
	// self-contained here to avoid a config→tmdb import).
	TMDBInteractiveReserveFrac float64
	// W19-1 — media downloader worker count (image.tmdb.org drain pool).
	// 0 or unset → application/media default (32). Env:
	// SEASONFILL_MEDIA_DOWNLOADER_WORKERS.
	MediaDownloaderWorkers int
	// W19-1 — on-demand media fetch budget. Single source of truth for
	// BOTH the handler's per-request wall budget (rest/media.go) AND the
	// fetcher's internal floor timeout (app/ondemand.go); driving both
	// from one value structurally prevents the pre-W19-1 drift where the
	// 1.5s floor silently capped the 2s wall budget. 0/unset → 10s
	// (floored at 1s). Env: SEASONFILL_MEDIA_ONDEMAND_BUDGET (seconds).
	MediaOnDemandBudget time.Duration
	// W19-3a — short sub-budget bounding the on-demand S3 Stat probe. A
	// HEAD on a MISSING object is pathologically slow on SeaweedFS (~21s),
	// so this caps the probe and lets a cold poster fall through to the
	// fetch path quickly. 0/unset → 800ms. Env: SEASONFILL_MEDIA_STAT_BUDGET
	// (milliseconds).
	MediaOnDemandStatBudget time.Duration
	// Story 1099 — serve-path S3 Get sub-budget. Bounds ONLY the
	// interactive GET /media/{hash} store.Get so a saturated store fails
	// fast into the SVG placeholder instead of hanging on minio's retry
	// stack. 0/unset → 1500ms (floored at 100ms). Env:
	// SEASONFILL_MEDIA_SERVE_GET_BUDGET (milliseconds).
	MediaServeGetBudget time.Duration
	// Story 1099 — bounded S3 in-flight caps (read/write split) enforced by
	// the meteredStore semaphores. Reads (Get/Stat) and writes (Put) have
	// independent ceilings so a downloader Put flood can never starve
	// interactive Gets. 0/unset/negative → 24 (read) / 12 (write). Envs:
	// SEASONFILL_MEDIA_S3_READ_INFLIGHT, SEASONFILL_MEDIA_S3_WRITE_INFLIGHT.
	MediaS3ReadInflight  int
	MediaS3WriteInflight int
	OMDBToken            string
	OMDBProxyURL         string
	OMDBProxyUser        string
	OMDBProxyPass        string
	TVDBToken            string
	TVDBProxyURL         string
	TVDBProxyUser        string
	TVDBProxyPass        string
}

// Lookup returns an EnvLookup-shaped closure over the ExternalServicesEnv
// block, so the application package never directly imports os.Getenv at
// runtime — tests can inject a fixture, production wires this method.
func (e ExternalServicesEnv) Lookup() func(string) string {
	m := map[string]string{
		"SEASONFILL_TMDB_TOKEN":      e.TMDBToken,
		"SEASONFILL_TMDB_PROXY_URL":  e.TMDBProxyURL,
		"SEASONFILL_TMDB_PROXY_USER": e.TMDBProxyUser,
		"SEASONFILL_TMDB_PROXY_PASS": e.TMDBProxyPass,
		"SEASONFILL_OMDB_TOKEN":      e.OMDBToken,
		"SEASONFILL_OMDB_PROXY_URL":  e.OMDBProxyURL,
		"SEASONFILL_OMDB_PROXY_USER": e.OMDBProxyUser,
		"SEASONFILL_OMDB_PROXY_PASS": e.OMDBProxyPass,
		"SEASONFILL_TVDB_TOKEN":      e.TVDBToken,
		"SEASONFILL_TVDB_PROXY_URL":  e.TVDBProxyURL,
		"SEASONFILL_TVDB_PROXY_USER": e.TVDBProxyUser,
		"SEASONFILL_TVDB_PROXY_PASS": e.TVDBProxyPass,
	}
	return func(name string) string { return m[name] }
}

type LogConfig struct {
	Level  string
	Format string
}

type HTTPConfig struct {
	Bind            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	Auth            Auth // Populated at runtime from DB auth settings

	// WebhookBaseURL is the configured fallback public base URL used by
	// the webhook reconciler when the per-request X-Forwarded-* context
	// value is empty (background 5-min reconcile, pod-restart lazy
	// reconcile). From SEASONFILL_WEBHOOK_BASE_URL. Empty when unset.
	WebhookBaseURL string
}

type DatabaseConfig struct {
	Driver   string
	SQLite   SQLiteConfig
	Postgres PostgresConfig
}

type SQLiteConfig struct {
	Path string
}

type PostgresConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// AuthBootstrap holds the env-only seed values for the first-run
// admin row and the HKDF input for AES-GCM. Everything else
// (session_ttl, secure_cookie, trusted_proxies) lives in DB.
type AuthBootstrap struct {
	APIKey           string
	WebUser          string
	WebPassword      string
	WebPasswordHash  string
	WebEmail         string
	OIDCClientSecret string
}

// MediaStoreConfig is the bootstrap-only block for the local media
// store (PRD v4 §6, §10.1). Mode "off" disables the store entirely
// (legacy hotlink behaviour) and is the default; "s3" reads S3.*; "fs"
// reads FSPath.
type MediaStoreConfig struct {
	Mode   string
	S3     MediaStoreS3Config
	FSPath string
}

// MediaStoreS3Config carries the SeaweedFS (or any S3-compatible)
// connection parameters. Region defaults to "us-east-1" — SeaweedFS
// ignores it but minio-go signs requests with it.
type MediaStoreS3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

var (
	ErrUnknownDriver       = errors.New("SEASONFILL_DATABASE_DRIVER must be sqlite or postgres")
	ErrSQLitePath          = errors.New("SEASONFILL_DATABASE_SQLITE_PATH is required when driver=sqlite")
	ErrPostgresDSN         = errors.New("SEASONFILL_DATABASE_POSTGRES_DSN is required when driver=postgres")
	ErrPasswordMutex       = errors.New("SEASONFILL_WEB_PASSWORD and SEASONFILL_WEB_PASSWORD_HASH are mutually exclusive")
	ErrMediaStoreMode      = errors.New("SEASONFILL_MEDIA_STORE_MODE must be one of off|s3|fs")
	ErrMediaStoreS3Missing = errors.New("SEASONFILL_S3_ENDPOINT, _BUCKET, _ACCESS_KEY and _SECRET_KEY are required when SEASONFILL_MEDIA_STORE_MODE=s3")
	ErrMediaStoreFSMissing = errors.New("SEASONFILL_MEDIA_FS_PATH is required when SEASONFILL_MEDIA_STORE_MODE=fs")
)

// FromEnv reads the Bootstrap from process environment.
func FromEnv() (*Bootstrap, error) {
	cfg := &Bootstrap{
		Log: LogConfig{
			Level:  getenv("SEASONFILL_LOG_LEVEL", "info"),
			Format: getenv("SEASONFILL_LOG_FORMAT", "json"),
		},
		HTTP: HTTPConfig{
			Bind:            getenv("SEASONFILL_HTTP_BIND", ":8080"),
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			WebhookBaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("SEASONFILL_WEBHOOK_BASE_URL")), "/"),
		},
		Database: DatabaseConfig{
			Driver: getenv("SEASONFILL_DATABASE_DRIVER", "sqlite"),
			SQLite: SQLiteConfig{
				Path: getenv("SEASONFILL_DATABASE_SQLITE_PATH", "./data/seasonfill.db"),
			},
			Postgres: PostgresConfig{
				DSN:             os.Getenv("SEASONFILL_DATABASE_POSTGRES_DSN"),
				MaxOpenConns:    getenvInt("SEASONFILL_DATABASE_POSTGRES_MAX_OPEN_CONNS", 10),
				MaxIdleConns:    getenvInt("SEASONFILL_DATABASE_POSTGRES_MAX_IDLE_CONNS", 5),
				ConnMaxLifetime: 5 * time.Minute,
			},
		},
		Auth: AuthBootstrap{
			APIKey:           os.Getenv("SEASONFILL_API_KEY"),
			WebUser:          getenv("SEASONFILL_WEB_USER", "admin"),
			WebPassword:      os.Getenv("SEASONFILL_WEB_PASSWORD"),
			WebPasswordHash:  os.Getenv("SEASONFILL_WEB_PASSWORD_HASH"),
			WebEmail:         os.Getenv("SEASONFILL_WEB_EMAIL"),
			OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		},
		MediaStore: MediaStoreConfig{
			Mode: getenv("SEASONFILL_MEDIA_STORE_MODE", "off"),
			S3: MediaStoreS3Config{
				Endpoint:  os.Getenv("SEASONFILL_S3_ENDPOINT"),
				Bucket:    getenv("SEASONFILL_S3_BUCKET", "seasonfill-media"),
				AccessKey: os.Getenv("SEASONFILL_S3_ACCESS_KEY"),
				SecretKey: os.Getenv("SEASONFILL_S3_SECRET_KEY"),
				Region:    getenv("SEASONFILL_S3_REGION", "us-east-1"),
				UseSSL:    getenvBool("SEASONFILL_S3_USE_SSL", true),
			},
			FSPath: getenvAllowEmpty("SEASONFILL_MEDIA_FS_PATH", "/data/media"),
		},
		ExternalServices: ExternalServicesEnv{
			TMDBToken:     os.Getenv("SEASONFILL_TMDB_TOKEN"),
			TMDBProxyURL:  os.Getenv("SEASONFILL_TMDB_PROXY_URL"),
			TMDBProxyUser: os.Getenv("SEASONFILL_TMDB_PROXY_USER"),
			TMDBProxyPass: os.Getenv("SEASONFILL_TMDB_PROXY_PASS"),
			TMDBRPS:       getenvFloat("SEASONFILL_TMDB_RPS", 0),
			// Story 346: SEASONFILL_TMDB_API_RPS supersedes the legacy
			// SEASONFILL_TMDB_RPS; when the new env is unset, fall back
			// to the legacy value so existing prod deployments don't
			// regress when this code lands. wiring/enrichment.go logs
			// a one-shot deprecation warning when only the legacy name
			// is set.
			TMDBAPIRPS:                 getenvFloat("SEASONFILL_TMDB_API_RPS", getenvFloat("SEASONFILL_TMDB_RPS", 0)),
			TMDBCDNRPS:                 getenvFloat("SEASONFILL_TMDB_CDN_RPS", 0),
			TMDBInteractiveReserveFrac: interactiveReserveFracFromEnv(),
			MediaDownloaderWorkers:     getenvInt("SEASONFILL_MEDIA_DOWNLOADER_WORKERS", 0),
			MediaOnDemandBudget:        mediaOnDemandBudgetFromEnv(),
			MediaOnDemandStatBudget:    mediaOnDemandStatBudgetFromEnv(),
			MediaServeGetBudget:        mediaServeGetBudgetFromEnv(),
			MediaS3ReadInflight:        getenvInt("SEASONFILL_MEDIA_S3_READ_INFLIGHT", 24),
			MediaS3WriteInflight:       getenvInt("SEASONFILL_MEDIA_S3_WRITE_INFLIGHT", 12),
			OMDBToken:                  os.Getenv("SEASONFILL_OMDB_TOKEN"),
			OMDBProxyURL:               os.Getenv("SEASONFILL_OMDB_PROXY_URL"),
			OMDBProxyUser:              os.Getenv("SEASONFILL_OMDB_PROXY_USER"),
			OMDBProxyPass:              os.Getenv("SEASONFILL_OMDB_PROXY_PASS"),
			TVDBToken:                  os.Getenv("SEASONFILL_TVDB_TOKEN"),
			TVDBProxyURL:               os.Getenv("SEASONFILL_TVDB_PROXY_URL"),
			TVDBProxyUser:              os.Getenv("SEASONFILL_TVDB_PROXY_USER"),
			TVDBProxyPass:              os.Getenv("SEASONFILL_TVDB_PROXY_PASS"),
		},
		Enrichment: EnrichmentConfig{
			ColdStartResweepInterval: coldStartResweepIntervalFromEnv(),
			// Story 347 — default ON; the env exists purely as a panic
			// kill-switch. Pinning "true" in chart values.yaml would
			// defeat that, so leave the env unset in production and let
			// the code default win.
			MediaUnifiedResolve:   getenvBool("SEASONFILL_MEDIA_UNIFIED_RESOLVE", true),
			SkeletonColdMediaSeed: getenvBool("SEASONFILL_SKELETON_COLD_MEDIA_SEED", true),
			// Story 1096 — worker/concurrency knobs. getenvInt returns the
			// default when the env is unset OR parses to <=0, so env=0/negative
			// naturally falls back to the >=1 default (no explicit clamp needed
			// here; the dispatcher/series-worker also defensively clamp at use).
			EnrichmentSeriesWorkers:     getenvInt("SEASONFILL_ENRICHMENT_SERIES_WORKERS", 2),
			EnrichmentPersonWorkers:     getenvInt("SEASONFILL_ENRICHMENT_PERSON_WORKERS", 1),
			EnrichmentSeasonConcurrency: getenvInt("SEASONFILL_ENRICHMENT_SEASON_CONCURRENCY", 4),
			// Story 1097 — background refresh drain levers. batch_size is a
			// plain getenvInt (0/negative → 50 default); the interval uses the
			// integer-SECONDS helper below.
			EnrichmentRefreshBatchSize: getenvInt("SEASONFILL_ENRICHMENT_REFRESH_BATCH_SIZE", 50),
			EnrichmentRefreshInterval:  refreshIntervalFromEnv(),
			Changes:                    changesConfigFromEnv(),
		},
		Discovery: DiscoveryConfig{
			// Story 568 A2 — default ON. The chart values.yaml pins the
			// env var to "true" for prod; operators flip it to "false"
			// only for a soft rollback (see story §7.1).
			PreWarmEnabled: getenvBool("SEASONFILL_DISCOVERY_PREWARM_ENABLED", true),
		},
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func getenv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func getenvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getenvBool(name string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

// getenvFloat parses a float64 env var (story 313). Returns def when
// the var is unset, empty, unparseable, OR <= 0. The "<=0 → default"
// rule lets the caller pass def=0 to mean "let the downstream package
// use its own default" — which is how SEASONFILL_TMDB_RPS works
// (config defaults to 0; infrastructure/tmdb interprets 0 as 50 rps).
func getenvFloat(name string, def float64) float64 {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// interactiveReserveFracFromEnv parses SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC
// (W110-5 / F-03). unset / empty / unparseable / <=0 → default 0.25; otherwise
// clamped to [0.05, 0.5]. Bounds mirror tmdb.{Default,Min,Max}InteractiveReserveFrac
// (duplicated here so config carries no config→tmdb import; tmdb.New re-clamps
// defensively).
func interactiveReserveFracFromEnv() float64 {
	const (
		def   = 0.25
		floor = 0.05
		ceil  = 0.5
	)
	v := getenvFloat("SEASONFILL_TMDB_INTERACTIVE_RESERVE_FRAC", 0)
	switch {
	case v <= 0:
		return def
	case v < floor:
		return floor
	case v > ceil:
		return ceil
	default:
		return v
	}
}

// getenvAllowEmpty returns the value of name if it is explicitly set
// (including the empty string), or def when it is unset. This lets
// MediaStore fs-mode validation surface "explicitly cleared" paths
// instead of silently falling back to the default.
func getenvAllowEmpty(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

// coldStartResweepIntervalFromEnv reads
// SEASONFILL_ENRICHMENT_COLDSTART_RESWEEP_SECONDS as an int. Story
// 318: <5 (including 0 / unset / unparseable) collapses to the 60s
// default. The floor exists so a misconfiguration cannot turn the
// re-sweep into a hot loop against the DB.
func coldStartResweepIntervalFromEnv() time.Duration {
	const def = 60 * time.Second
	const floor = 5 * time.Second
	secs := getenvInt("SEASONFILL_ENRICHMENT_COLDSTART_RESWEEP_SECONDS", 0)
	if secs <= 0 {
		return def
	}
	d := time.Duration(secs) * time.Second
	if d < floor {
		return floor
	}
	return d
}

// refreshIntervalFromEnv reads SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS
// as an int number of seconds (Story 1097). 0 / unset / unparseable → the
// 30m default. Floored at 60s so a misconfigured tiny interval cannot turn
// the background refresh sweep into a tick-storm against the worker + TMDB.
func refreshIntervalFromEnv() time.Duration {
	const def = 30 * time.Minute
	const floor = 60 * time.Second
	secs := getenvInt("SEASONFILL_ENRICHMENT_REFRESH_INTERVAL_SECONDS", 0)
	if secs <= 0 {
		return def
	}
	d := time.Duration(secs) * time.Second
	if d < floor {
		return floor
	}
	return d
}

// changesConfigFromEnv parses the Wave 2 TMDB /tv/changes poller env block
// (plan §10). Clamps applied AFTER the getenvInt defaults so a bad value can
// never disable the poller nor exceed the API window cap.
func changesConfigFromEnv() ChangesConfig {
	return ChangesConfig{
		Enabled:      getenvBool("SEASONFILL_TMDB_CHANGES_ENABLED", false),
		PollInterval: changesPollIntervalFromEnv(),
		// getenvInt floors <=0/unparseable to the default; upper-clamp to 7.
		OverlapDays: clampInt(getenvInt("SEASONFILL_TMDB_CHANGES_OVERLAP_DAYS", 1), 1, 7),
		// default 14 (= API cap); clamp [1..14].
		LookbackDays: clampInt(getenvInt("SEASONFILL_TMDB_CHANGES_MAX_LOOKBACK_DAYS", 14), 1, 14),
		// pagination safety valve; getenvInt floors <=0 to 200, no upper clamp.
		PageCap: getenvInt("SEASONFILL_TMDB_CHANGES_PAGE_CAP", 200),
		// IN-chunk size; clamp both bounds [50..900].
		MarkBatch: clampInt(getenvInt("SEASONFILL_TMDB_CHANGES_MARK_BATCH", 500), 50, 900),
	}
}

// changesPollIntervalFromEnv reads SEASONFILL_TMDB_CHANGES_POLL_INTERVAL as a
// Go duration string (e.g. "8h", "90m"). unset / empty / unparseable / <=0 →
// the 8h default (plan §0-G8). Min-clamped to 1h so a misconfigured tiny
// interval cannot turn the firehose poll into a tick-storm (plan §10).
func changesPollIntervalFromEnv() time.Duration {
	const def = 8 * time.Hour
	const floor = time.Hour
	v := strings.TrimSpace(os.Getenv("SEASONFILL_TMDB_CHANGES_POLL_INTERVAL"))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	if d < floor {
		return floor
	}
	return d
}

// clampInt bounds v to [lo, hi]. lo/hi assumed lo<=hi.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// mediaOnDemandBudgetFromEnv reads SEASONFILL_MEDIA_ONDEMAND_BUDGET as an
// int number of seconds (W19-1). 0 / unset / unparseable → the 10s
// default. Floored at 1s so a misconfiguration cannot collapse the
// on-demand fetch window. This single value drives BOTH the handler wall
// budget and the fetcher floor timeout — see MediaOnDemandBudget.
func mediaOnDemandBudgetFromEnv() time.Duration {
	const def = 10 * time.Second
	const floor = 1 * time.Second
	secs := getenvInt("SEASONFILL_MEDIA_ONDEMAND_BUDGET", 0)
	if secs <= 0 {
		return def
	}
	d := time.Duration(secs) * time.Second
	if d < floor {
		return floor
	}
	return d
}

// mediaOnDemandStatBudgetFromEnv reads SEASONFILL_MEDIA_STAT_BUDGET as an
// int number of milliseconds (W19-3a). 0 / unset / unparseable → the
// 800ms default. Floored at 50ms so a misconfiguration cannot collapse
// the stat probe to zero. This bounds the on-demand S3 Stat sub-context
// so a slow HEAD on a missing object falls through to the fetch path
// quickly — see MediaOnDemandStatBudget.
func mediaOnDemandStatBudgetFromEnv() time.Duration {
	const def = 800 * time.Millisecond
	const floor = 50 * time.Millisecond
	ms := getenvInt("SEASONFILL_MEDIA_STAT_BUDGET", 0)
	if ms <= 0 {
		return def
	}
	d := time.Duration(ms) * time.Millisecond
	if d < floor {
		return floor
	}
	return d
}

// mediaServeGetBudgetFromEnv reads SEASONFILL_MEDIA_SERVE_GET_BUDGET as an
// int number of milliseconds (Story 1099). 0 / unset / unparseable → the
// 1500ms default. Floored at 100ms so a misconfiguration cannot collapse
// the serve Get window. Bounds ONLY the interactive serve-path store.Get —
// see MediaServeGetBudget.
func mediaServeGetBudgetFromEnv() time.Duration {
	const def = 1500 * time.Millisecond
	const floor = 100 * time.Millisecond
	ms := getenvInt("SEASONFILL_MEDIA_SERVE_GET_BUDGET", 0)
	if ms <= 0 {
		return def
	}
	d := time.Duration(ms) * time.Millisecond
	if d < floor {
		return floor
	}
	return d
}

// (Validate lives in validate.go — see §3.)
var _ = fmt.Errorf
