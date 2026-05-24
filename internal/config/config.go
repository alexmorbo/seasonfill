package config

import (
	"errors"
	"fmt"
	"time"
)

type Config struct {
	Log             LogConfig             `koanf:"log"`
	HTTP            HTTPConfig            `koanf:"http"`
	Cron            CronConfig            `koanf:"cron"`
	Database        DatabaseConfig        `koanf:"database"`
	DryRun          bool                  `koanf:"dry_run"`
	Scan            ScanConfig            `koanf:"scan"`
	GlobalRateLimit GlobalRateLimitConfig `koanf:"global_rate_limit"`
	SonarrInstances []SonarrInstance      `koanf:"sonarr_instances"`
}

// GlobalRateLimitConfig — second-tier cross-instance limiter (PRD §8.1).
// Defaults set in `Defaults()`. Zero values disable the limiter.
type GlobalRateLimitConfig struct {
	RPM   int `koanf:"rpm"`
	Burst int `koanf:"burst"`
}

type LogConfig struct {
	Level  string `koanf:"level"`
	Format string `koanf:"format"`
}

type HTTPConfig struct {
	Bind            string        `koanf:"bind"`
	ReadTimeout     time.Duration `koanf:"read_timeout"`
	WriteTimeout    time.Duration `koanf:"write_timeout"`
	IdleTimeout     time.Duration `koanf:"idle_timeout"`
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	Auth            AuthConfig    `koanf:"auth"`
}

// AuthConfig holds the admin HTTP auth surface.
//
// SecureCookie sets the cookie's Secure flag — true ONLY when
// serving over HTTPS (e.g. behind an ingress with TLS terminated).
// Default false so http://localhost dev works (browsers drop
// Secure cookies on HTTP). M1 review-fix: do NOT alias Enabled.
//
// TrustedProxies lists CIDRs/IPs whose X-Forwarded-For headers we
// honor when resolving the client IP (used by login + webhook rate
// limits). Default ["127.0.0.1", "::1"] — only localhost trusted.
// Empty list disables XFF trust entirely (uses RemoteAddr).
type AuthConfig struct {
	Enabled         bool          `koanf:"enabled"`
	APIKey          string        `koanf:"api_key"`
	SecureCookie    bool          `koanf:"secure_cookie"`
	SessionTTL      time.Duration `koanf:"session_ttl"`
	WebUser         string        `koanf:"web_user"`
	WebPassword     string        `koanf:"web_password"`
	WebPasswordHash string        `koanf:"web_password_hash"`
	TrustedProxies  []string      `koanf:"trusted_proxies"`
}

type CronConfig struct {
	Enabled  bool          `koanf:"enabled"`
	Schedule string        `koanf:"schedule"`
	OnStart  bool          `koanf:"on_start"`
	Jitter   time.Duration `koanf:"jitter"`
}

type DatabaseConfig struct {
	Driver   string         `koanf:"driver"`
	SQLite   SQLiteConfig   `koanf:"sqlite"`
	Postgres PostgresConfig `koanf:"postgres"`
}

type SQLiteConfig struct {
	Path string `koanf:"path"`
}

type PostgresConfig struct {
	DSN             string        `koanf:"dsn"`
	MaxOpenConns    int           `koanf:"max_open_conns"`
	MaxIdleConns    int           `koanf:"max_idle_conns"`
	ConnMaxLifetime time.Duration `koanf:"conn_max_lifetime"`
}

type ScanConfig struct {
	ShutdownGrace time.Duration `koanf:"shutdown_grace"`
	CooldownSweep time.Duration `koanf:"cooldown_sweep"`
}

// SonarrInstance — per-instance Sonarr configuration.
//
// DryRun is a nullable bool so YAML can distinguish "unset" from "false".
// koanf handles `*bool` natively for bare YAML bools (`dry_run: true`,
// `dry_run: false`) and leaves the pointer nil when the key is absent.
// Per D-2.6 the instance-level override wins if non-nil; otherwise we fall
// back to the global `Config.DryRun`.
type SonarrInstance struct {
	Name   string `koanf:"name"`
	URL    string `koanf:"url"`
	APIKey string `koanf:"api_key"`
	// Timeout applies to every Sonarr API call EXCEPT
	// `SearchReleases` (which uses SearchTimeout). Default 10s
	// via ApplyInstanceDefaults — fast enough for fail-fast
	// health checks on /api/v3/system/status.
	Timeout time.Duration `koanf:"timeout"`
	// SearchTimeout applies ONLY to `SearchReleases`
	// (GET /api/v3/release?seriesId=…&seasonNumber=…), which
	// triggers interactive indexer search via Prowlarr. Slow
	// indexers (RuTracker) routinely take 20–45s; default is
	// Timeout*6 (= 60s on a 10s base) via ApplyInstanceDefaults.
	// Operator may set this independently of Timeout.
	SearchTimeout time.Duration `koanf:"search_timeout"`
	DryRun        *bool         `koanf:"dry_run"`
	// Mode = "auto" (default) or "manual". Manual instances are
	// excluded from the cron sweep but reachable via UI/API
	// (`RunInstance`).
	Mode        string            `koanf:"mode"`
	Tags        TagsConfig        `koanf:"tags"`
	Search      SearchConfig      `koanf:"search"`
	Ranking     RankingConfig     `koanf:"ranking"`
	Limits      LimitsConfig      `koanf:"limits"`
	RateLimit   RateLimitConfig   `koanf:"rate_limit"`
	Cooldown    CooldownConfig    `koanf:"cooldown"`
	Retry       RetryConfig       `koanf:"retry"`
	HealthCheck HealthCheckConfig `koanf:"health_check"`
}

// HealthCheckConfig — per-instance watchdog recheck intervals (D-2.3).
// RecheckIntervalAuth applies to UnavailableAuth (needs human action, default
// 5m). RecheckIntervalNetwork applies to UnavailableNetwork and
// UnavailableUnknown (transient, may recover quickly, default 1m).
type HealthCheckConfig struct {
	RecheckIntervalAuth    time.Duration `koanf:"recheck_interval_auth"`
	RecheckIntervalNetwork time.Duration `koanf:"recheck_interval_network"`
}

type RateLimitConfig struct {
	RPS   float64 `koanf:"rps"`
	Burst int     `koanf:"burst"`
}

type TagsConfig struct {
	Mode    string   `koanf:"mode"`
	Include []string `koanf:"include"`
	Exclude []string `koanf:"exclude"`
}

type SearchConfig struct {
	RequireAllAired      bool `koanf:"require_all_aired"`
	SkipSpecials         bool `koanf:"skip_specials"`
	SkipAnime            bool `koanf:"skip_anime"`
	MinCustomFormatScore int  `koanf:"min_custom_format_score"`
}

type RankingConfig struct {
	IndexerPriorityEnabled bool    `koanf:"indexer_priority_enabled"`
	OriginBonus            float64 `koanf:"origin_bonus"`
}

type LimitsConfig struct {
	ScanMaxSeries   int `koanf:"scan_max_series"`
	MaxGrabsPerScan int `koanf:"max_grabs_per_scan"`
}

type CooldownConfig struct {
	Mode                  string        `koanf:"mode"`
	SeriesAfterGrab       time.Duration `koanf:"series_after_grab"`
	GUIDAfterFailedGrab   time.Duration `koanf:"guid_after_failed_grab"`
	GUIDAfterFailedImport time.Duration `koanf:"guid_after_failed_import"`
}

type RetryConfig struct {
	MaxAttempts    int           `koanf:"max_attempts"`
	InitialBackoff time.Duration `koanf:"initial_backoff"`
	MaxBackoff     time.Duration `koanf:"max_backoff"`
}

// DryRunFor returns the effective dry-run flag for one instance.
// Instance override (if set) wins over the global flag, per D-2.6.
func (c *Config) DryRunFor(inst SonarrInstance) bool {
	if inst.DryRun != nil {
		return *inst.DryRun
	}
	return c.DryRun
}

// Defaults — sane defaults baked into the binary.
//
// DryRun defaults to TRUE so a first-run user pulling the image does NOT
// issue real grabs without explicit opt-in. Operators set `dry_run: false`
// (globally or per-instance) only after verifying scan decisions in logs
// or in the `decisions` DB table.
func Defaults() *Config {
	return &Config{
		Log: LogConfig{Level: "info", Format: "json"},
		HTTP: HTTPConfig{
			Bind:            ":8080",
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			Auth: AuthConfig{
				Enabled:        true,
				WebUser:        "admin",
				SessionTTL:     12 * time.Hour,
				TrustedProxies: []string{"127.0.0.1", "::1"},
			},
		},
		Cron: CronConfig{
			Enabled:  true,
			Schedule: "0 */6 * * *",
			OnStart:  false,
			Jitter:   time.Minute,
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			SQLite: SQLiteConfig{Path: "./data/seasonfill.db"},
			Postgres: PostgresConfig{
				MaxOpenConns:    10,
				MaxIdleConns:    5,
				ConnMaxLifetime: 5 * time.Minute,
			},
		},
		DryRun: true,
		Scan: ScanConfig{
			ShutdownGrace: 60 * time.Second,
			CooldownSweep: 15 * time.Minute,
		},
		GlobalRateLimit: GlobalRateLimitConfig{
			RPM:   30,
			Burst: 10,
		},
	}
}

// ApplyInstanceDefaults populates omitted instance-level knobs with sane defaults.
func (c *Config) ApplyInstanceDefaults() {
	for i := range c.SonarrInstances {
		inst := &c.SonarrInstances[i]
		// Timeout default (10s) — fast enough for health checks
		// against /api/v3/system/status, slow enough for routine
		// /api/v3/series + /api/v3/episode listings under load.
		if inst.Timeout <= 0 {
			inst.Timeout = 10 * time.Second
		}
		// SearchTimeout default — `Timeout*6` so the ratio stays
		// constant if the operator bumps the base. 60s on the
		// default 10s base covers the observed p99 on a slow
		// indexer chain (Sonarr → Prowlarr → RuTracker).
		if inst.SearchTimeout <= 0 {
			inst.SearchTimeout = inst.Timeout * 6
		}
		if inst.Cooldown.Mode == "" {
			inst.Cooldown.Mode = "smart"
		}
		if inst.Cooldown.SeriesAfterGrab == 0 {
			inst.Cooldown.SeriesAfterGrab = 24 * time.Hour
		}
		if inst.Cooldown.GUIDAfterFailedGrab == 0 {
			inst.Cooldown.GUIDAfterFailedGrab = 72 * time.Hour
		}
		if inst.Cooldown.GUIDAfterFailedImport == 0 {
			inst.Cooldown.GUIDAfterFailedImport = 48 * time.Hour
		}
		if inst.Retry.MaxAttempts == 0 {
			inst.Retry.MaxAttempts = 3
		}
		if inst.Retry.InitialBackoff == 0 {
			inst.Retry.InitialBackoff = time.Second
		}
		if inst.Retry.MaxBackoff == 0 {
			inst.Retry.MaxBackoff = 30 * time.Second
		}
		if inst.Limits.MaxGrabsPerScan == 0 {
			inst.Limits.MaxGrabsPerScan = 10
		}
		// Clamp non-positive recheck intervals to the documented defaults.
		// Negative values would otherwise cause the watchdog's intervalFor()
		// to return a negative duration, making the gate `now.Sub(last) < due`
		// pass on every tick and recheck fire every loop. Deferred-item #2.
		if inst.HealthCheck.RecheckIntervalAuth <= 0 {
			inst.HealthCheck.RecheckIntervalAuth = 5 * time.Minute
		}
		if inst.HealthCheck.RecheckIntervalNetwork <= 0 {
			inst.HealthCheck.RecheckIntervalNetwork = time.Minute
		}
		if inst.Mode == "" {
			inst.Mode = "auto"
		}
	}
}

var (
	ErrNoInstances       = errors.New("at least one sonarr instance is required")
	ErrInstanceURL       = errors.New("sonarr instance url is required")
	ErrInstanceName      = errors.New("sonarr instance name is required")
	ErrInstanceAPIKey    = errors.New("sonarr instance api_key is required")
	ErrInstanceMode      = errors.New("sonarr instance mode must be one of: auto, manual")
	ErrUnknownDriver     = errors.New("unknown database driver, expected sqlite or postgres")
	ErrAuthKeyRequired   = errors.New("http.auth.api_key is required when auth.enabled=true")
	ErrAuthPasswordMutex = errors.New("http.auth.web_password and http.auth.web_password_hash are mutually exclusive")
	ErrPostgresDSN       = errors.New("database.postgres.dsn is required when driver=postgres")
	ErrSQLitePath        = errors.New("database.sqlite.path is required when driver=sqlite")
)

func (c *Config) Validate() error {
	switch c.Database.Driver {
	case "sqlite":
		if c.Database.SQLite.Path == "" {
			return ErrSQLitePath
		}
	case "postgres":
		if c.Database.Postgres.DSN == "" {
			return ErrPostgresDSN
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownDriver, c.Database.Driver)
	}

	if c.HTTP.Auth.Enabled && c.HTTP.Auth.APIKey == "" {
		return ErrAuthKeyRequired
	}

	if c.HTTP.Auth.WebPassword != "" && c.HTTP.Auth.WebPasswordHash != "" {
		return ErrAuthPasswordMutex
	}

	if len(c.SonarrInstances) == 0 {
		return ErrNoInstances
	}
	for i, inst := range c.SonarrInstances {
		if inst.Name == "" {
			return fmt.Errorf("instance #%d: %w", i, ErrInstanceName)
		}
		if inst.URL == "" {
			return fmt.Errorf("instance %q: %w", inst.Name, ErrInstanceURL)
		}
		if inst.APIKey == "" {
			return fmt.Errorf("instance %q: %w", inst.Name, ErrInstanceAPIKey)
		}
		switch inst.Mode {
		case "", "auto", "manual":
		default:
			return fmt.Errorf("instance %q: %w (got %q)", inst.Name, ErrInstanceMode, inst.Mode)
		}
	}
	return nil
}
