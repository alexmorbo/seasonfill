package config

import (
	"errors"
	"fmt"
	"time"
)

type Config struct {
	Log             LogConfig        `koanf:"log"`
	HTTP            HTTPConfig       `koanf:"http"`
	Cron            CronConfig       `koanf:"cron"`
	Database        DatabaseConfig   `koanf:"database"`
	DryRun          bool             `koanf:"dry_run"`
	Scan            ScanConfig       `koanf:"scan"`
	SonarrInstances []SonarrInstance `koanf:"sonarr_instances"`
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

type AuthConfig struct {
	Enabled bool   `koanf:"enabled"`
	APIKey  string `koanf:"api_key"`
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
}

type SonarrInstance struct {
	Name      string          `koanf:"name"`
	URL       string          `koanf:"url"`
	APIKey    string          `koanf:"api_key"`
	Timeout   time.Duration   `koanf:"timeout"`
	Tags      TagsConfig      `koanf:"tags"`
	Search    SearchConfig    `koanf:"search"`
	Ranking   RankingConfig   `koanf:"ranking"`
	Limits    LimitsConfig    `koanf:"limits"`
	RateLimit RateLimitConfig `koanf:"rate_limit"`
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
	ScanMaxSeries int `koanf:"scan_max_series"`
}

func Defaults() *Config {
	return &Config{
		Log: LogConfig{Level: "info", Format: "json"},
		HTTP: HTTPConfig{
			Bind:            ":8080",
			ReadTimeout:     15 * time.Second,
			WriteTimeout:    30 * time.Second,
			IdleTimeout:     60 * time.Second,
			ShutdownTimeout: 10 * time.Second,
			Auth:            AuthConfig{Enabled: true},
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
		},
	}
}

var (
	ErrNoInstances     = errors.New("at least one sonarr instance is required")
	ErrInstanceURL     = errors.New("sonarr instance url is required")
	ErrInstanceName    = errors.New("sonarr instance name is required")
	ErrInstanceAPIKey  = errors.New("sonarr instance api_key is required")
	ErrUnknownDriver   = errors.New("unknown database driver, expected sqlite or postgres")
	ErrAuthKeyRequired = errors.New("http.auth.api_key is required when auth.enabled=true")
	ErrPostgresDSN     = errors.New("database.postgres.dsn is required when driver=postgres")
	ErrSQLitePath      = errors.New("database.sqlite.path is required when driver=sqlite")
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
	}
	return nil
}
