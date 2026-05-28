// Package database wires GORM to the chosen driver.
//
// IMPORTANT: this package intentionally imports github.com/glebarez/sqlite —
// the pure-Go SQLite driver backed by modernc.org/sqlite. Do NOT replace it
// with gorm.io/driver/sqlite. That driver pulls mattn/go-sqlite3 which
// requires CGO; our Dockerfile and Makefile both build with CGO_ENABLED=0
// and the binary would crash at gorm.Open with
// "go-sqlite3 requires cgo to work". The gorm.io/driver/sqlite module
// lingers as an indirect dependency of gorm.io/datatypes; no .go file
// imports it, so it cannot be selected at runtime. See Phase 1 delta D-1.1.
package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/alexmorbo/seasonfill/internal/config"
)

var ErrUnknownDriver = errors.New("unknown database driver")

func Open(cfg config.DatabaseConfig) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		TranslateError: true,
	}

	switch cfg.Driver {
	case "sqlite":
		if cfg.SQLite.Path == "" {
			return nil, fmt.Errorf("sqlite path is empty")
		}
		if dir := filepath.Dir(cfg.SQLite.Path); dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("mkdir %s: %w", dir, err)
			}
		}
		db, err := gorm.Open(sqlite.Open(cfg.SQLite.Path), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("open sqlite: %w", err)
		}
		// Defense in depth: restrict to owner r/w only. Ignore on
		// in-memory paths (":memory:" or "file::memory:...").
		if cfg.SQLite.Path != ":memory:" && !strings.HasPrefix(cfg.SQLite.Path, "file::memory:") {
			if chmodErr := os.Chmod(cfg.SQLite.Path, 0o600); chmodErr != nil {
				db.Logger.Warn(context.Background(), "sqlite chmod failed: %v", chmodErr)
			}
		}
		return db, nil

	case "postgres":
		if cfg.Postgres.DSN == "" {
			return nil, fmt.Errorf("postgres dsn is empty")
		}
		db, err := gorm.Open(postgres.Open(cfg.Postgres.DSN), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("open postgres (dsn=%s): %s",
				redactDSN(cfg.Postgres.DSN), scrubPassword(err.Error(), cfg.Postgres.DSN))
		}
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("postgres driver: %w", err)
		}
		if cfg.Postgres.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(cfg.Postgres.MaxOpenConns)
		}
		if cfg.Postgres.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(cfg.Postgres.MaxIdleConns)
		}
		if cfg.Postgres.ConnMaxLifetime > 0 {
			sqlDB.SetConnMaxLifetime(cfg.Postgres.ConnMaxLifetime)
		}
		return db, nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownDriver, cfg.Driver)
	}
}

func Ping(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("driver: %w", err)
	}
	return sqlDB.Ping()
}
