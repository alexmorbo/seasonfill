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
	Enabled          bool
	APIKey           string
	SessionTTL       time.Duration
	SecureCookie     bool
	TrustedProxies   []string
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
	// the on-demand fetcher. 0 or unset → application/media default
	// (100). The CDN is Cloudflare-backed with no published per-IP
	// limit; the cap exists only to bound a runaway worker pool.
	// Env: SEASONFILL_TMDB_CDN_RPS.
	TMDBAPIRPS    float64
	TMDBCDNRPS    float64
	OMDBToken     string
	OMDBProxyURL  string
	OMDBProxyUser string
	OMDBProxyPass string
	TVDBToken     string
	TVDBProxyURL  string
	TVDBProxyUser string
	TVDBProxyPass string
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
			TMDBAPIRPS:    getenvFloat("SEASONFILL_TMDB_API_RPS", getenvFloat("SEASONFILL_TMDB_RPS", 0)),
			TMDBCDNRPS:    getenvFloat("SEASONFILL_TMDB_CDN_RPS", 0),
			OMDBToken:     os.Getenv("SEASONFILL_OMDB_TOKEN"),
			OMDBProxyURL:  os.Getenv("SEASONFILL_OMDB_PROXY_URL"),
			OMDBProxyUser: os.Getenv("SEASONFILL_OMDB_PROXY_USER"),
			OMDBProxyPass: os.Getenv("SEASONFILL_OMDB_PROXY_PASS"),
			TVDBToken:     os.Getenv("SEASONFILL_TVDB_TOKEN"),
			TVDBProxyURL:  os.Getenv("SEASONFILL_TVDB_PROXY_URL"),
			TVDBProxyUser: os.Getenv("SEASONFILL_TVDB_PROXY_USER"),
			TVDBProxyPass: os.Getenv("SEASONFILL_TVDB_PROXY_PASS"),
		},
		Enrichment: EnrichmentConfig{
			ColdStartResweepInterval: coldStartResweepIntervalFromEnv(),
			// Story 347 — default ON; the env exists purely as a panic
			// kill-switch. Pinning "true" in chart values.yaml would
			// defeat that, so leave the env unset in production and let
			// the code default win.
			MediaUnifiedResolve: getenvBool("SEASONFILL_MEDIA_UNIFIED_RESOLVE", true),
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

// (Validate lives in validate.go — see §3.)
var _ = fmt.Errorf
