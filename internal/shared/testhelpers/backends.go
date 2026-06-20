package testhelpers

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// envPostgresEnable opts the Postgres backend into AllBackends(t).
// Default (unset) keeps the helper SQLite-only so `make test-race` and
// the default `go test ./...` stay Docker-free. CI's
// `make test-integration-postgres` target sets this to "1" so the
// dual-backend matrix actually exercises Postgres.
//
// SEASONFILL_TEST_POSTGRES_DSN (handled inside StartPostgres) also
// turns the Postgres branch on implicitly — pointing at an existing
// DB is enough signal that the caller wants both backends.
const envPostgresEnable = "SEASONFILL_TEST_POSTGRES_ENABLE"

// Backend is one entry in the dual-backend dispatch table.
//
//   - Name is the t.Run sub-test label; pick "sqlite" or "postgres"
//     verbatim so the failure trail names the dialect.
//   - NewDB returns an isolated *gorm.DB with all embedded migrations
//     applied. Each call gives the test its own database — Postgres
//     branch creates a fresh per-test DB inside the shared container,
//     SQLite branch opens a fresh `:memory:` handle. Both are safe to
//     use under t.Parallel().
type Backend struct {
	Name  string
	NewDB func(testing.TB) *gorm.DB
}

// AllBackends returns the backend dispatch set for the current run.
//
// Default behavior (no env vars set): returns just the SQLite backend.
// This keeps `make test-race` / `make test` fast and Docker-free, which
// matches the §6 D-0 §6.4174 contract — Postgres is an opt-in lane,
// not a default.
//
// Opt-in: set SEASONFILL_TEST_POSTGRES_ENABLE=1 (or
// SEASONFILL_TEST_POSTGRES_DSN to a libpq URL) to add the Postgres
// backend. `make test-integration-postgres` sets the enable flag.
//
// Usage pattern (the pilot in series_cache_repository_test.go locks
// this for A-4-3 mechanical rollout):
//
//	for _, backend := range testhelpers.AllBackends(t) {
//	    t.Run(backend.Name, func(t *testing.T) {
//	        t.Parallel()
//	        db := backend.NewDB(t)
//	        // ...original test body...
//	    })
//	}
//
// Note on parallelism: AllBackends does NOT call t.Parallel inside the
// returned closures — that's the caller's choice. Tests that intentionally
// serialize (TestMain-driven schema caches, anything touching a shared
// fake clock) stay correct under the dispatcher.
func AllBackends(t testing.TB) []Backend {
	t.Helper()

	backends := []Backend{
		{Name: "sqlite", NewDB: newSQLiteDB},
	}

	if !postgresEnabled() {
		return backends
	}

	pc := StartPostgres(t)
	backends = append(backends, Backend{
		Name:  "postgres",
		NewDB: pc.NewDB,
	})
	return backends
}

// postgresEnabled is true when either the enable flag or the DSN
// override env var is set. Empty strings count as unset to keep the
// CI matrix's "var defined but blank" edge case from accidentally
// flipping the backend on without Docker available.
func postgresEnabled() bool {
	if v := os.Getenv(envPostgresEnable); v != "" && v != "0" && v != "false" {
		return true
	}
	if v := os.Getenv(envOverrideDSN); v != "" {
		return true
	}
	return false
}

// newSQLiteDB opens a fresh `:memory:` GORM handle and runs the full
// embedded migration set against it.
//
// Each call returns a brand-new isolated DB. We pin SetMaxOpenConns(1)
// so every query lands on the same underlying SQLite database — without
// this, the database/sql connection pool would hand out independent
// `:memory:` databases per connection and tests would see empty results
// at random.
//
// Performance note: this calls database.Migrate end-to-end (all 44
// migrations) per test, which is the slow path. The repositories test
// package owns a per-process schema-cache shortcut (scan_repository_test.go
// TestMain, story 314) that's ~80x faster. A-4-3 (story 424) will lift
// that cache into testhelpers so AllBackends can share it; until then
// the pilot pays ~80ms × N tests, which is acceptable for ~37 tests.
func newSQLiteDB(t testing.TB) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}
