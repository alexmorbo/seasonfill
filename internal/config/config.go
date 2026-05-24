package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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
	Log      LogConfig
	HTTP     HTTPConfig
	Database DatabaseConfig
	Auth     AuthBootstrap
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
	APIKey          string
	WebUser         string
	WebPassword     string
	WebPasswordHash string
}

var (
	ErrUnknownDriver = errors.New("SEASONFILL_DATABASE_DRIVER must be sqlite or postgres")
	ErrSQLitePath    = errors.New("SEASONFILL_DATABASE_SQLITE_PATH is required when driver=sqlite")
	ErrPostgresDSN   = errors.New("SEASONFILL_DATABASE_POSTGRES_DSN is required when driver=postgres")
	ErrPasswordMutex = errors.New("SEASONFILL_WEB_PASSWORD and SEASONFILL_WEB_PASSWORD_HASH are mutually exclusive")
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
			APIKey:          os.Getenv("SEASONFILL_API_KEY"),
			WebUser:         getenv("SEASONFILL_WEB_USER", "admin"),
			WebPassword:     os.Getenv("SEASONFILL_WEB_PASSWORD"),
			WebPasswordHash: os.Getenv("SEASONFILL_WEB_PASSWORD_HASH"),
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

// (Validate lives in validate.go — see §3.)
func (c *Bootstrap) validateStub() error { return nil } // why: keep linker happy in mid-edit
var _ = fmt.Errorf
