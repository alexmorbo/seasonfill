package testhelpers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	// pgx stdlib registers the "pgx" driver for database/sql. We use the
	// driver directly for admin-DSN CREATE/DROP DATABASE operations against
	// the shared container. Explicit import is per story 422 §3.5 Option B:
	// self-documenting, no transitive surprises.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// postgresImage is the pinned image for the shared test container.
// PRD §6.2780 calls for PG 17-alpine; CNPG prod is at 16; future bump
// to 18 will be staged here first.
const postgresImage = "postgres:17-alpine"

// envOverrideDSN is the prior-art env var name from
// infrastructure/database/migrations_test.go:299. When set, the helper
// skips container boot entirely and uses the supplied DSN as the
// admin connection.
const envOverrideDSN = "SEASONFILL_TEST_POSTGRES_DSN"

// PostgresContainer wraps a singleton testcontainers Postgres instance plus
// per-test DB cloning. Acquire via StartPostgres(t).
type PostgresContainer struct {
	// DSN is the admin connection string to the postgres superuser DB.
	// Used to issue CREATE DATABASE / DROP DATABASE for per-test isolation.
	DSN string

	// templateName is the per-process pre-migrated template DB. Per-test DBs
	// are cloned from it via CREATE DATABASE ... TEMPLATE (file-copy speed)
	// instead of re-running all migrations each time.
	templateName string

	container testcontainers.Container
}

var (
	pgOnce      sync.Once
	pgSingleton *PostgresContainer
	pgErr       error
)

// StartPostgres returns the suite-shared Postgres container. The container
// boots once per process (sync.Once) and is left running for the lifetime
// of the test process; testcontainers' Ryuk reaper sidecar handles cleanup
// if the process dies abnormally.
//
// Override behavior: when SEASONFILL_TEST_POSTGRES_DSN is set no container
// is started and the env DSN is used directly. This lets local dev run
// against an existing Postgres without Docker (matches prior-art convention
// from infrastructure/database/migrations_test.go:299).
//
// Failure mode: if container boot fails (Docker unavailable, image pull
// timeout, etc.) the helper calls t.Fatalf. Tests that want to tolerate
// missing Docker should pre-check and call t.Skip before invoking this
// helper.
func StartPostgres(t testing.TB) *PostgresContainer {
	t.Helper()

	pgOnce.Do(func() {
		if envDSN := os.Getenv(envOverrideDSN); envDSN != "" {
			pgSingleton = &PostgresContainer{DSN: envDSN}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			container, err := tcpostgres.Run(ctx, postgresImage,
				tcpostgres.WithDatabase("postgres"),
				tcpostgres.WithUsername("seasonfill"),
				tcpostgres.WithPassword("seasonfill"),
				testcontainers.WithWaitStrategy(
					wait.ForLog("database system is ready to accept connections").
						WithOccurrence(2).
						WithStartupTimeout(60*time.Second),
				),
			)
			if err != nil {
				pgErr = fmt.Errorf("start postgres container: %w", err)
				return
			}

			adminDSN, err := container.ConnectionString(ctx, "sslmode=disable")
			if err != nil {
				_ = container.Terminate(ctx)
				pgErr = fmt.Errorf("derive admin dsn: %w", err)
				return
			}

			pgSingleton = &PostgresContainer{DSN: adminDSN, container: container}
		}

		buildTemplate(pgSingleton)
	})

	if pgErr != nil {
		t.Fatalf("postgres testcontainer: %v", pgErr)
	}
	return pgSingleton
}

// buildTemplate migrates a single per-process template DB that every per-test
// DB is later cloned from. The template name carries an 8-byte-hex suffix so
// that `go test ./pkgA ./pkgB` — which spawns one process per package against
// a shared external Postgres — never collides on the template DB.
//
// On any error it sets the package-level pgErr so StartPostgres' t.Fatalf
// fires; callers never observe a pgSingleton without a usable template.
func buildTemplate(pc *PostgresContainer) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		pgErr = fmt.Errorf("template rand: %w", err)
		return
	}
	templateName := "seasonfill_test_template_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", pc.DSN)
	if err != nil {
		pgErr = fmt.Errorf("template open admin: %w", err)
		return
	}
	if _, err := admin.ExecContext(
		context.Background(),
		fmt.Sprintf("CREATE DATABASE %q", templateName),
	); err != nil {
		_ = admin.Close()
		pgErr = fmt.Errorf("create template db %s: %w", templateName, err)
		return
	}
	_ = admin.Close()

	db, err := gorm.Open(postgres.Open(pc.dsn(templateName)), &gorm.Config{})
	if err != nil {
		pgErr = fmt.Errorf("template gorm open: %w", err)
		return
	}
	if err := database.Migrate(db); err != nil {
		pgErr = fmt.Errorf("template migrate: %w", err)
		return
	}

	// The template must have zero open connections: CREATE DATABASE ... TEMPLATE
	// fails with "source database is being accessed by other users" otherwise.
	// Clones serialize on the template's brief ACCESS EXCLUSIVE lock, which is
	// milliseconds for a small schema DB.
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}

	pc.templateName = templateName
}

// NewDB creates a fresh isolated database inside the shared container by
// cloning the pre-migrated template (CREATE DATABASE ... TEMPLATE, file-copy
// speed) and returns a *gorm.DB pointing at it. The database is DROPped on
// t.Cleanup.
//
// Database naming: seasonfill_test_<8-byte-hex>. The randomness eliminates
// any test-order coupling so t.Parallel() is safe.
func (pc *PostgresContainer) NewDB(t testing.TB) *gorm.DB {
	t.Helper()

	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	dbName := "seasonfill_test_" + hex.EncodeToString(raw[:])

	admin, err := sql.Open("pgx", pc.DSN)
	if err != nil {
		t.Fatalf("open admin dsn: %v", err)
	}
	defer func() { _ = admin.Close() }()

	if _, err := admin.ExecContext(
		context.Background(),
		fmt.Sprintf("CREATE DATABASE %q TEMPLATE %q", dbName, pc.templateName),
	); err != nil {
		t.Fatalf("create db %s: %v", dbName, err)
	}

	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", pc.DSN)
		if err != nil {
			return
		}
		defer func() { _ = cleanup.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Terminate any leftover connections to the per-test DB before
		// DROP; otherwise Postgres refuses with "database is being
		// accessed by other users".
		_, _ = cleanup.ExecContext(ctx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
			   WHERE datname = $1 AND pid <> pg_backend_pid()`,
			dbName)
		_, _ = cleanup.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	})

	db, err := gorm.Open(postgres.Open(pc.dsn(dbName)), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	return db
}

// dsn returns a connection string for the named per-test DB, derived from
// the shared container's admin DSN by swapping the dbname segment.
//
// The admin DSN is in libpq URL form (postgres://user:pass@host:port/postgres?...).
// We swap the path segment between the host:port and the query string.
func (pc *PostgresContainer) dsn(dbName string) string {
	return swapDBName(pc.DSN, dbName)
}

// swapDBName replaces the path component of a libpq URL with the supplied
// database name. The path is the segment between the first "/" after the
// authority and the "?" (or end of string). Falls back to a query-string
// dbname override if the URL has no path component.
func swapDBName(dsn, dbName string) string {
	schemeIdx := strings.Index(dsn, "://")
	if schemeIdx < 0 {
		return dsn
	}
	rest := dsn[schemeIdx+3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		// No path; append.
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "dbname=" + dbName
	}
	pathStart := schemeIdx + 3 + slashIdx + 1
	tail := dsn[pathStart:]
	queryIdx := strings.Index(tail, "?")
	if queryIdx < 0 {
		return dsn[:pathStart] + dbName
	}
	return dsn[:pathStart] + dbName + tail[queryIdx:]
}
