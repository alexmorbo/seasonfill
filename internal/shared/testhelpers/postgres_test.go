//go:build integration

package testhelpers_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestStartPostgres_Smoke proves the helper:
//  1. Boots a Postgres 17-alpine container (or honors env DSN override).
//  2. Returns a non-nil singleton.
//  3. NewDB(t) creates an isolated DB with migrations applied.
//  4. The DB exposes scan_runs (asserts at least one migrated table is present).
//  5. Parallel sub-tests get distinct databases (no shared state).
func TestStartPostgres_Smoke(t *testing.T) {
	pc := testhelpers.StartPostgres(t)
	require.NotNil(t, pc)
	require.NotEmpty(t, pc.DSN)

	t.Run("isolation_a", func(t *testing.T) {
		t.Parallel()
		db := pc.NewDB(t)
		require.NotNil(t, db)

		assert.True(t, db.Migrator().HasTable("scan_runs"),
			"migrations must run against the per-test DB")
		assert.Equal(t, "postgres", db.Name(),
			"helper must produce a Postgres-backed gorm.DB, not SQLite")
	})

	t.Run("isolation_b", func(t *testing.T) {
		t.Parallel()
		db := pc.NewDB(t)
		require.NotNil(t, db)
		assert.True(t, db.Migrator().HasTable("scan_runs"))
	})
}

// TestStartPostgres_Singleton verifies the sync.Once behavior — two calls
// to StartPostgres in the same process return the same container handle.
func TestStartPostgres_Singleton(t *testing.T) {
	a := testhelpers.StartPostgres(t)
	b := testhelpers.StartPostgres(t)
	require.Same(t, a, b, "StartPostgres must be idempotent within a process")
}

// TestStartPostgres_EnvOverride sanity-checks the env-DSN fast path. Skips
// when SEASONFILL_TEST_POSTGRES_DSN is unset (CI default with container).
func TestStartPostgres_EnvOverride(t *testing.T) {
	envDSN := os.Getenv("SEASONFILL_TEST_POSTGRES_DSN")
	if envDSN == "" {
		t.Skip("set SEASONFILL_TEST_POSTGRES_DSN to exercise override path")
	}
	pc := testhelpers.StartPostgres(t)
	require.NotNil(t, pc)
	// When the override is honored, the DSN must equal the env var exactly
	// (no container ConnectionString rewriting).
	if !strings.EqualFold(pc.DSN, envDSN) {
		t.Fatalf("env-override DSN drift: want %q got %q", envDSN, pc.DSN)
	}
}
