package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
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
		return db, nil

	case "postgres":
		if cfg.Postgres.DSN == "" {
			return nil, fmt.Errorf("postgres dsn is empty")
		}
		db, err := gorm.Open(postgres.Open(cfg.Postgres.DSN), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("open postgres: %w", err)
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
