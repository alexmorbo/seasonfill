//go:build integration

// Story 455 (D-1-2) — verifies the generated migration applies cleanly
// on BOTH backends via golang-migrate (the runtime path; NOT atlas) and
// rolls back cleanly via the .down.sql.
//
// D-0 compliance:
//   - SQLite is exercised in-process with a fresh :memory: DB per test.
//   - Postgres lane gates on SEASONFILL_TEST_POSTGRES_ENABLE (matches
//     testhelpers.AllBackends — same opt-in surface, so CI's
//     test-integration-postgres target covers it).
//   - Unique UUIDs for series.title avoid collisions across parallel
//     runs against the shared Postgres container.
//   - Explicit error-pair coverage: FK violation MUST fail, with the
//     specific reason left dialect-side (PG SQLSTATE 23503, SQLite
//     "FOREIGN KEY constraint failed" — both are sufficient signal).
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
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	migratesqlite "github.com/alexmorbo/seasonfill/infrastructure/database/migratesqlite"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

type backend struct {
	name      string
	driverSQL string
	migrate   func(t testing.TB) (*sql.DB, *migrate.Migrate, func())
}

func d12Backends(t *testing.T) []backend {
	t.Helper()
	out := []backend{
		{name: "sqlite", driverSQL: "sqlite", migrate: openSQLite},
	}
	if os.Getenv("SEASONFILL_TEST_POSTGRES_ENABLE") != "" ||
		os.Getenv("SEASONFILL_TEST_POSTGRES_DSN") != "" {
		out = append(out, backend{
			name:      "postgres",
			driverSQL: "pgx",
			migrate:   openPostgres,
		})
	}
	return out
}

func TestD12_CoreSeriesMigrationRoundTrip(t *testing.T) {
	for _, b := range d12Backends(t) {
		t.Run(b.name, func(t *testing.T) {
			// AllBackends-style: do NOT t.Parallel() inside the inner
			// loop; the shared Postgres container is happier when
			// per-test DB lifecycles are serialized.
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			db, m, cleanup := b.migrate(t)
			t.Cleanup(cleanup)

			// UP — apply 000001_core_series.up.sql.
			require.NoError(t, m.Up(), "Up should apply the migration on a clean DB")

			// Smoke: series, seasons, episodes exist + a basic insert
			// path works.
			seriesTitle := "d1-2-" + uuid.NewString()
			_, err := db.ExecContext(ctx, insertSeriesSQL(b.name),
				seriesTitle, "stub", false, "[]")
			require.NoError(t, err, "insert into series should succeed")

			// FK enforcement: orphan episode INSERT must fail.
			_, err = db.ExecContext(ctx, insertOrphanEpisodeSQL(b.name),
				999999, 1, 1)
			require.Error(t, err, "FK violation expected for episodes.series_id = 999999")

			// DOWN — apply 000001_core_series.down.sql.
			require.NoError(t, m.Down(), "Down should drop the migration cleanly")

			// After down, series/seasons/episodes must not exist.
			_, err = db.ExecContext(ctx, "SELECT 1 FROM series LIMIT 1")
			require.Error(t, err, "series table should be dropped after Down")
		})
	}
}

// openSQLite builds an in-memory SQLite DB and binds golang-migrate to
// the dialect-specific migrations directory using our vendored sqlite
// driver (avoids the double-Register("sqlite") collision documented in
// infrastructure/database/migratesqlite).
func openSQLite(t testing.TB) (*sql.DB, *migrate.Migrate, func()) {
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

	src, err := (&file.File{}).Open("file://" + migrationsDir(t, "sqlite"))
	require.NoError(t, err)

	m, err := migrate.NewWithInstance("file", src, "sqlite", driver)
	require.NoError(t, err)

	cleanup := func() {
		_, _ = m.Close()
		_ = db.Close()
	}
	return db, m, cleanup
}

// openPostgres carves a fresh per-test DB inside the shared
// testcontainers Postgres instance, applies the migration via
// golang-migrate, and returns the (*sql.DB, *migrate.Migrate) pair
// rebinding the connection to the new DB.
func openPostgres(t testing.TB) (*sql.DB, *migrate.Migrate, func()) {
	t.Helper()

	pc := testhelpers.StartPostgres(t)

	var raw [8]byte
	_, err := rand.Read(raw[:])
	require.NoError(t, err)
	dbName := "seasonfill_d1_2_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", pc.DSN)
	require.NoError(t, err)
	defer func() { _ = admin.Close() }()

	_, err = admin.ExecContext(context.Background(),
		fmt.Sprintf("CREATE DATABASE %q", dbName))
	require.NoError(t, err)

	dsn := swapPGDBName(pc.DSN, dbName)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	require.NoError(t, db.PingContext(pingCtx))

	driver, err := migratepg.WithInstance(db, &migratepg.Config{
		MigrationsTable: "schema_migrations",
	})
	require.NoError(t, err)

	src, err := (&file.File{}).Open("file://" + migrationsDir(t, "postgres"))
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

// migrationsDir resolves the absolute path to
// infrastructure/database/migrations/{postgres,sqlite}/.
// runtime.Caller(0) gives this test file path; we walk up two levels
// to reach the repo root.
func migrationsDir(t testing.TB, dialect string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(repoRoot, "infrastructure", "database", "migrations", dialect)
}

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

func insertOrphanEpisodeSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO episodes (series_id, season_number, episode_number, created_at, updated_at)
		        VALUES ($1, $2, $3, now(), now())`
	case "sqlite":
		return `INSERT INTO episodes (series_id, season_number, episode_number, created_at, updated_at)
		        VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}

// swapPGDBName mirrors the unexported helper inside testhelpers
// (postgres.go). Inlined here so the integration test stays
// self-contained.
func swapPGDBName(dsn, dbName string) string {
	scheme := "://"
	idx := indexOf(dsn, scheme)
	if idx < 0 {
		return dsn
	}
	rest := dsn[idx+len(scheme):]
	slash := indexOf(rest, "/")
	if slash < 0 {
		sep := "?"
		if indexOf(dsn, "?") >= 0 {
			sep = "&"
		}
		return dsn + sep + "dbname=" + dbName
	}
	pathStart := idx + len(scheme) + slash + 1
	tail := dsn[pathStart:]
	q := indexOf(tail, "?")
	if q < 0 {
		return dsn[:pathStart] + dbName
	}
	return dsn[:pathStart] + dbName + tail[q:]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
