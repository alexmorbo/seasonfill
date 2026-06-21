//go:build integration

// d1_helpers shares the dual-backend test helpers used by every D-1
// integration test. Extracted from d1_2_core_series_apply_test.go
// during D-1-3a (story 456a) to avoid copy-paste as the test surface
// grows beyond the first migration.
//
// All exported names live in package integration and are referenced
// across multiple _test.go files in the same package.
package integration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	migratesqlite "github.com/alexmorbo/seasonfill/infrastructure/database/migratesqlite"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// d1Backend pairs a dialect name with its golang-migrate setup func.
type d1Backend struct {
	name      string
	driverSQL string
	migrate   func(t testing.TB) (*sql.DB, *migrate.Migrate, func())
}

// allD1Backends mirrors testhelpers.AllBackends but binds the
// dialect-specific golang-migrate driver + DSN dance. Same opt-in
// surface (SEASONFILL_TEST_POSTGRES_ENABLE / SEASONFILL_TEST_POSTGRES_DSN).
func allD1Backends(t *testing.T) []d1Backend {
	t.Helper()
	out := []d1Backend{
		{name: "sqlite", driverSQL: "sqlite", migrate: openD1SQLite},
	}
	if os.Getenv("SEASONFILL_TEST_POSTGRES_ENABLE") != "" ||
		os.Getenv("SEASONFILL_TEST_POSTGRES_DSN") != "" {
		out = append(out, d1Backend{
			name:      "postgres",
			driverSQL: "pgx",
			migrate:   openD1Postgres,
		})
	}
	return out
}

// openD1SQLite builds an in-memory SQLite DB and binds golang-migrate
// to the dialect-specific migrations directory using our vendored
// sqlite driver (avoids the double-Register("sqlite") collision
// documented in infrastructure/database/migratesqlite).
func openD1SQLite(t testing.TB) (*sql.DB, *migrate.Migrate, func()) {
	t.Helper()

	// `_pragma=foreign_keys(1)` activates SQLite foreign-key
	// enforcement on glebarez/go-sqlite (the driver registered as
	// "sqlite" by the GORM dialector pulled via testhelpers).
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, db.PingContext(ctx))

	driver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{
		MigrationsTable: "schema_migrations",
		NoTxWrap:        true,
	})
	require.NoError(t, err)

	src, err := (&file.File{}).Open("file://" + d1MigrationsDir(t, "sqlite"))
	require.NoError(t, err)

	m, err := migrate.NewWithInstance("file", src, "sqlite", driver)
	require.NoError(t, err)

	cleanup := func() {
		_, _ = m.Close()
		_ = db.Close()
	}
	return db, m, cleanup
}

// openD1Postgres carves a fresh per-test DB inside the shared
// testcontainers Postgres instance, applies the migration via
// golang-migrate, and returns the (*sql.DB, *migrate.Migrate) pair
// rebinding the connection to the new DB.
func openD1Postgres(t testing.TB) (*sql.DB, *migrate.Migrate, func()) {
	t.Helper()

	pc := testhelpers.StartPostgres(t)

	var raw [8]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	dbName := "seasonfill_d1_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", pc.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()

	_, err = admin.ExecContext(context.Background(),
		fmt.Sprintf("CREATE DATABASE %q", dbName))
	require.NoError(t, err)

	dsn := d1SwapPGDBName(pc.DSN, dbName)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	require.NoError(t, db.PingContext(pingCtx))

	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		MigrationsTable: "schema_migrations",
	})
	require.NoError(t, err)

	src, err := (&file.File{}).Open("file://" + d1MigrationsDir(t, "postgres"))
	require.NoError(t, err)

	m, err := migrate.NewWithInstance("file", src, "postgres", driver)
	require.NoError(t, err)

	cleanup := func() {
		_, _ = m.Close()
		_ = db.Close()

		// Drop the per-test DB via a fresh admin connection (the
		// previous one is closed; testcontainers Postgres expects
		// the per-test DB to be dropped before pg_stat_activity
		// settles).
		drop, derr := sql.Open("pgx", pc.DSN)
		if derr != nil {
			return
		}
		defer func() { _ = drop.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = drop.ExecContext(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			   WHERE datname = $1 AND pid <> pg_backend_pid()`, dbName)
		_, _ = drop.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	}
	return db, m, cleanup
}

// d1MigrationsDir resolves the absolute path to
// infrastructure/database/migrations/{postgres,sqlite}/.
// runtime.Caller(0) gives this test file path; we walk up two levels
// to reach the repo root.
func d1MigrationsDir(t testing.TB, dialect string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(repoRoot, "infrastructure", "database", "migrations", dialect)
}

// d1SwapPGDBName mirrors the unexported helper inside testhelpers
// (postgres.go). Inlined here so the integration test stays
// self-contained.
func d1SwapPGDBName(dsn, dbName string) string {
	scheme := "://"
	idx := d1IndexOf(dsn, scheme)
	if idx < 0 {
		return dsn
	}
	rest := dsn[idx+len(scheme):]
	slash := d1IndexOf(rest, "/")
	if slash < 0 {
		sep := "?"
		if d1IndexOf(dsn, "?") >= 0 {
			sep = "&"
		}
		return dsn + sep + "dbname=" + dbName
	}
	pathStart := idx + len(scheme) + slash + 1
	tail := dsn[pathStart:]
	q := d1IndexOf(tail, "?")
	if q < 0 {
		return dsn[:pathStart] + dbName
	}
	return dsn[:pathStart] + dbName + tail[q:]
}

func d1IndexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// insertSeriesSQL returns a parametric INSERT for the series table
// shared across D-1 integration tests. Used by both
// d1_2_core_series_apply_test.go and d1_3a_i18n_texts_apply_test.go.
func insertSeriesSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO series (title, hydration, in_production, origin_countries, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, now(), now())`
	case "sqlite":
		return `INSERT INTO series (title, hydration, in_production, origin_countries, created_at, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
