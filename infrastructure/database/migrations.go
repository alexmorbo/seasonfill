package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgdriver "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"

	sqlitedriver "github.com/alexmorbo/seasonfill/infrastructure/database/internal/migratesqlite"
)

//go:embed migrations/postgres/*.sql migrations/sqlite/*.sql
var migrationsFS embed.FS

const (
	baselineVersion = 1
	latestVersion   = 25
)

// Migrate applies all pending versioned migrations. Signature is preserved
// so callers (server bootstrap, tests) keep working; the body dispatches
// on the underlying dialect because Postgres (prod) and SQLite (tests) use
// driver-specific SQL.
//
// Pre-existing prod databases were created by the legacy GORM auto-migrate
// path and have no schema_migrations table. When we detect application
// tables already present but no schema_migrations, we stamp baseline as
// applied so golang-migrate treats the existing schema as version 1.
// Idempotent.
func Migrate(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}

	dialect := db.Name()
	ctx := context.Background()

	// Stamp must run before constructing the migrate driver because driver
	// construction creates schema_migrations as a side effect, which would
	// mask the "app tables present, no bookkeeping" condition we need to
	// detect here.
	if err := stampBaselineIfNeeded(ctx, sqlDB, dialect); err != nil {
		return fmt.Errorf("stamp baseline: %w", err)
	}

	driver, subdir, err := newMigrateDriver(dialect, sqlDB)
	if err != nil {
		return err
	}

	subFS, err := fs.Sub(migrationsFS, "migrations/"+subdir)
	if err != nil {
		return fmt.Errorf("sub fs %s: %w", subdir, err)
	}
	src, err := iofs.New(subFS, ".")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, dialect, driver)
	if err != nil {
		return fmt.Errorf("migrate instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func newMigrateDriver(dialect string, sqlDB *sql.DB) (database.Driver, string, error) {
	switch dialect {
	case "postgres":
		d, err := pgdriver.WithInstance(sqlDB, &pgdriver.Config{})
		if err != nil {
			return nil, "", fmt.Errorf("postgres driver: %w", err)
		}
		return d, "postgres", nil
	case "sqlite":
		d, err := sqlitedriver.WithInstance(sqlDB, &sqlitedriver.Config{})
		if err != nil {
			return nil, "", fmt.Errorf("sqlite driver: %w", err)
		}
		return d, "sqlite", nil
	default:
		return nil, "", fmt.Errorf("unsupported dialect: %s", dialect)
	}
}

// stampBaselineIfNeeded inserts the current schema version into
// schema_migrations when the DB already contains application tables but
// no migrations bookkeeping. We do a direct INSERT instead of m.Force(n)
// because Force historically left dirty=true in some library versions; a
// raw stamp is unambiguous and lets m.Up() proceed without a clean step.
// The stamped version is determined by probing for known v2 columns so
// that a database which already has v2 schema is not re-stamped at v1.
func stampBaselineIfNeeded(ctx context.Context, sqlDB *sql.DB, dialect string) error {
	hasMigrations, err := tableExists(ctx, sqlDB, dialect, "schema_migrations")
	if err != nil {
		return err
	}
	if hasMigrations {
		return nil
	}
	hasApp, err := tableExists(ctx, sqlDB, dialect, "scan_runs")
	if err != nil {
		return err
	}
	if !hasApp {
		return nil
	}
	// Detect whether v2/v3 columns already exist so we stamp at the right
	// version. This prevents re-applying migrations on a DB that lost its
	// schema_migrations bookkeeping but retained the schema.
	version := baselineVersion
	hasV2, err := columnExists(ctx, sqlDB, dialect, "runtime_config", "auth_mode")
	if err != nil {
		return err
	}
	if hasV2 {
		version = 2
	}
	hasV3, err := columnExists(ctx, sqlDB, dialect, "runtime_config", "oidc_issuer")
	if err != nil {
		return err
	}
	if hasV3 {
		version = 11
	}
	// 043b: Detect v12 by checking for size_bytes on grab_records.
	hasV12, err := columnExists(ctx, sqlDB, dialect, "grab_records", "size_bytes")
	if err != nil {
		return err
	}
	if hasV12 {
		version = 12
	}
	// 071: Detect v18 by checking for last_aired_at on series_cache.
	hasV18, err := columnExists(ctx, sqlDB, dialect, "series_cache", "last_aired_at")
	if err != nil {
		return err
	}
	if hasV18 {
		version = 18
	}
	// 082: Detect v19 by checking for qbit_public_url on instance_qbit_settings.
	hasV19, err := columnExists(ctx, sqlDB, dialect, "instance_qbit_settings", "qbit_public_url")
	if err != nil {
		return err
	}
	if hasV19 {
		version = 19
	}
	// 091a / F-P2-2: Detect v21 by checking for intent on decisions.
	// v20 (error_detail widen) cannot be detected via columnExists because
	// the type change leaves the column name unchanged, so the prior
	// v19 stamp covers both v19 and v20 — v20 will be reapplied as a
	// no-op widen on legacy DBs, which is intentional.
	hasV21, err := columnExists(ctx, sqlDB, dialect, "decisions", "intent")
	if err != nil {
		return err
	}
	if hasV21 {
		version = 21
	}
	// 107: Detect v22 by checking for guid_rewrites on runtime_config.
	hasV22, err := columnExists(ctx, sqlDB, dialect, "runtime_config", "guid_rewrites")
	if err != nil {
		return err
	}
	if hasV22 {
		version = 22
	}
	// 201 (S-1): Detect v24 by checking for the media_assets table.
	// v23 (cooldowns.reason widen) is a type change that leaves the column
	// name unchanged, so columnExists cannot detect it; the v22 stamp
	// covers both v22 and v23 — v23 will be reapplied as a no-op widen
	// on legacy DBs, which is intentional.
	hasV24, err := tableExists(ctx, sqlDB, dialect, "media_assets")
	if err != nil {
		return err
	}
	if hasV24 {
		version = 24
	}
	// 202 (S-2): Detect v25 by checking for the external_service_settings table.
	hasV25, err := tableExists(ctx, sqlDB, dialect, "external_service_settings")
	if err != nil {
		return err
	}
	if hasV25 {
		version = 25
	}
	createStmt, insertStmt := stampStatements(dialect)
	if createStmt == "" {
		return fmt.Errorf("unsupported dialect: %s", dialect)
	}
	if _, err := sqlDB.ExecContext(ctx, createStmt); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	if _, err := sqlDB.ExecContext(ctx, insertStmt, version); err != nil {
		return fmt.Errorf("stamp baseline row: %w", err)
	}
	return nil
}

func stampStatements(dialect string) (createStmt, insertStmt string) {
	switch dialect {
	case "postgres":
		return `CREATE TABLE IF NOT EXISTS schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`,
			`INSERT INTO schema_migrations (version, dirty) VALUES ($1, false) ON CONFLICT (version) DO NOTHING`
	case "sqlite":
		return `CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool); CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version)`,
			`INSERT OR IGNORE INTO schema_migrations (version, dirty) VALUES (?, 0)`
	default:
		return "", ""
	}
}

func columnExists(ctx context.Context, sqlDB *sql.DB, dialect, table, column string) (bool, error) {
	var q string
	switch dialect {
	case "postgres":
		q = `SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`
	case "sqlite":
		// pragma_table_info returns one row per column; filter by name.
		q = `SELECT 1 FROM pragma_table_info(?) WHERE name = ?`
	default:
		return false, fmt.Errorf("unsupported dialect: %s", dialect)
	}
	var one int
	if err := sqlDB.QueryRowContext(ctx, q, table, column).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("probe column %s.%s: %w", table, column, err)
	}
	return true, nil
}

func tableExists(ctx context.Context, sqlDB *sql.DB, dialect, name string) (bool, error) {
	var q string
	switch dialect {
	case "postgres":
		q = `SELECT 1 FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`
	case "sqlite":
		q = `SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`
	default:
		return false, fmt.Errorf("unsupported dialect: %s", dialect)
	}
	var one int
	err := sqlDB.QueryRowContext(ctx, q, name).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("probe %s: %w", name, err)
	}
	return true, nil
}
