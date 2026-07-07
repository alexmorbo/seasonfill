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
	"sort"
	"testing"
	"time"

	atlasschema "ariga.io/atlas/sql/schema"
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

// d1AcceptanceTablesPostgres is the canonical Postgres-side table inventory
// asserted by TestD1_Acceptance_TableInventory. Sourced from
// schema.Schema(Postgres) on 2026-06-21 (story 461 / D-1-8); extended
// in D-4 story 465b (scan_runs migration 000015), then D-6 story 467a
// (decisions / cooldowns / origin_releases), D-6 story 467c
// (qbit_settings / qbit_torrents / qbit_torrent_events /
// torrent_series_map), D-7 story 468c (media_assets migration
// 000019), N-2a story 502 (discovery_lists migration 000021), and E-1
// B3a (season_texts, always-on i18n text table alongside
// series_texts/episode_texts).
// 56 tables in total — schema_migrations (golang-migrate tracker) is
// excluded; it is not part of the seasonfill domain.
//
// Names are the same on both backends — the SQLite list is identical.
var d1AcceptanceTablesPostgres = []string{
	"app_config",
	"app_secret",
	"content_ratings",
	"cooldowns",
	"decisions",
	"discovery_lists",
	"download_links",
	"enrichment_errors",
	"episode_grabs",
	"episode_states",
	"episode_texts",
	"episodes",
	"external_ids",
	"external_service_config",
	"external_service_quota_state",
	"genres",
	"genres_i18n",
	"grab_records",
	"instance_secret",
	"keywords",
	"keywords_i18n",
	"media_assets",
	"networks",
	"origin_releases",
	"people",
	"people_texts",
	"person_biographies",
	"person_credits",
	"person_credits_texts",
	"production_companies",
	"qbit_settings",
	"qbit_torrent_events",
	"qbit_torrents",
	"scan_runs",
	"season_media_texts",
	"season_stats",
	"season_texts",
	"seasons",
	"series",
	"series_cache",
	"series_companies",
	"series_genres",
	"series_images",
	"series_keywords",
	"series_media_texts",
	"series_networks",
	"series_recommendations",
	"series_texts",
	"sonarr_instance",
	"sonarr_instance_settings",
	"torrent_series_map",
	"user_instance_tags",
	"users",
	"videos",
	"watchdog_blacklist",
	"watchdog_state",
}

// liveTableNames returns the set of D-1 tables visible in the live DB
// after a full Up. Excludes the golang-migrate tracker
// (schema_migrations) and SQLite internal tables. Sorted ascending so
// the caller can directly ElementsMatch against d1AcceptanceTablesPostgres.
func liveTableNames(t testing.TB, ctx context.Context, db *sql.DB, dialect string) []string {
	t.Helper()
	var q string
	switch dialect {
	case "postgres":
		q = `SELECT table_name FROM information_schema.tables
		     WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
		     AND table_type = 'BASE TABLE'`
	case "sqlite":
		q = `SELECT name FROM sqlite_master
		     WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`
	default:
		t.Fatalf("liveTableNames: unknown dialect %q", dialect)
	}
	rows, err := db.QueryContext(ctx, q)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		out = append(out, n)
	}
	require.NoError(t, rows.Err())
	sort.Strings(out)
	return out
}

// liveColumnNames returns the sorted list of column names for `table`
// on the live DB. Postgres uses information_schema; SQLite uses the
// PRAGMA table_info virtual table.
func liveColumnNames(t testing.TB, ctx context.Context, db *sql.DB, dialect, table string) []string {
	t.Helper()
	out := []string{}
	switch dialect {
	case "postgres":
		rows, err := db.QueryContext(ctx,
			`SELECT column_name FROM information_schema.columns
			 WHERE table_schema = 'public' AND table_name = $1`, table)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var n string
			require.NoError(t, rows.Scan(&n))
			out = append(out, n)
		}
		require.NoError(t, rows.Err())
	case "sqlite":
		// PRAGMA table_info is a virtual table; identifier interpolation
		// is unavoidable (sqlite refuses parameter binding here). Table
		// names come from the canonical schema, not user input.
		rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, table))
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var (
				cid     int
				name    string
				ctype   string
				notnull int
				dflt    sql.NullString
				pk      int
			)
			require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk))
			out = append(out, name)
		}
		require.NoError(t, rows.Err())
	default:
		t.Fatalf("liveColumnNames: unknown dialect %q", dialect)
	}
	sort.Strings(out)
	return out
}

// indexDefinition returns the dialect-native definition string for
// indexName. Postgres returns `pg_indexes.indexdef` (full CREATE INDEX);
// SQLite returns `sqlite_master.sql`. The caller asserts substrings (no
// strict equality — atlas may reorder column lists or normalize the
// predicate).
func indexDefinition(t testing.TB, ctx context.Context, db *sql.DB, dialect, indexName string) string {
	t.Helper()
	switch dialect {
	case "postgres":
		var def string
		err := db.QueryRowContext(ctx,
			`SELECT indexdef FROM pg_indexes WHERE indexname = $1`, indexName).Scan(&def)
		require.NoErrorf(t, err, "pg_indexes lookup for %q", indexName)
		return def
	case "sqlite":
		var def sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type='index' AND name = ?`, indexName).Scan(&def)
		require.NoErrorf(t, err, "sqlite_master lookup for %q", indexName)
		require.True(t, def.Valid, "index %q has no recorded sql in sqlite_master", indexName)
		return def.String
	}
	t.Fatalf("indexDefinition: unknown dialect %q", dialect)
	return ""
}

// columnNamesFromTable returns the sorted column names declared on tbl
// by the schema package. Used to cross-check against liveColumnNames.
func columnNamesFromTable(tbl *atlasschema.Table) []string {
	out := make([]string, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return out
}

// insertSeriesSQL returns a parametric INSERT for the series table
// shared across D-1 integration tests. Used by both
// d1_2_core_series_apply_test.go and d1_3a_i18n_texts_apply_test.go.
func insertSeriesSQL(driver string) string {
	switch driver {
	case "postgres":
		return `INSERT INTO series (original_title, hydration, in_production, origin_countries, created_at, updated_at)
		        VALUES ($1, $2, $3, $4, now(), now())`
	case "sqlite":
		return `INSERT INTO series (original_title, hydration, in_production, origin_countries, created_at, updated_at)
		        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	}
	panic("unknown driver " + driver)
}
