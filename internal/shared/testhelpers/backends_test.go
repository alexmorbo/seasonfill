package testhelpers_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestAllBackends_DefaultIsSQLiteOnly proves the no-env default returns
// exactly one backend (sqlite). This is the contract the local fast
// loop (`make test-race`) depends on — surprise Docker starts are the
// failure mode this test guards against.
//
// We force-unset the env vars for the duration of this test in case
// the operator has them set in their .envrc.
func TestAllBackends_DefaultIsSQLiteOnly(t *testing.T) {
	withEnv(t, "SEASONFILL_TEST_POSTGRES_ENABLE", "")
	withEnv(t, "SEASONFILL_TEST_POSTGRES_DSN", "")

	backends := testhelpers.AllBackends(t)
	require.Len(t, backends, 1)
	assert.Equal(t, "sqlite", backends[0].Name)

	db := backends[0].NewDB(t)
	require.NotNil(t, db)
	assert.Equal(t, "sqlite", db.Name())
	assert.True(t, db.Migrator().HasTable("series"),
		"sqlite backend must run all migrations")
}

// TestAllBackends_PostgresEnabledAddsBackend proves the env flag
// flips the second backend on. Skipped when neither the enable flag
// nor the override DSN is set — matches StartPostgres's opt-in shape
// from story 422.
func TestAllBackends_PostgresEnabledAddsBackend(t *testing.T) {
	if os.Getenv("SEASONFILL_TEST_POSTGRES_ENABLE") == "" &&
		os.Getenv("SEASONFILL_TEST_POSTGRES_DSN") == "" {
		t.Skip("set SEASONFILL_TEST_POSTGRES_ENABLE=1 (or _DSN) to exercise the postgres branch")
	}

	backends := testhelpers.AllBackends(t)
	require.Len(t, backends, 2)
	assert.Equal(t, "sqlite", backends[0].Name)
	assert.Equal(t, "postgres", backends[1].Name)

	for _, b := range backends {
		t.Run(b.Name, func(t *testing.T) {
			db := b.NewDB(t)
			require.NotNil(t, db)
			assert.True(t, db.Migrator().HasTable("series"),
				"%s backend must run all migrations", b.Name)
		})
	}
}

// TestAllBackends_IsolationAcrossCalls proves NewDB returns independent
// databases per call. Writing to one must not be visible from another —
// otherwise t.Parallel() inside the dispatcher would deadlock-or-race.
func TestAllBackends_IsolationAcrossCalls(t *testing.T) {
	withEnv(t, "SEASONFILL_TEST_POSTGRES_ENABLE", "")
	withEnv(t, "SEASONFILL_TEST_POSTGRES_DSN", "")

	backends := testhelpers.AllBackends(t)
	require.Len(t, backends, 1)

	dbA := backends[0].NewDB(t)
	dbB := backends[0].NewDB(t)
	require.NotSame(t, dbA, dbB, "each NewDB call must return a fresh handle")

	// Writes to dbA do not bleed into dbB. We use series as the
	// witness because every migrated DB has it and it's writable
	// with no FK dependencies.
	require.NoError(t, dbA.Exec(`INSERT INTO series
		(original_title, hydration, origin_countries)
		VALUES ('Test', 'stub', '[]')`).Error)

	var countA, countB int64
	require.NoError(t, dbA.Raw(`SELECT COUNT(*) FROM series`).Scan(&countA).Error)
	require.NoError(t, dbB.Raw(`SELECT COUNT(*) FROM series`).Scan(&countB).Error)
	assert.Equal(t, int64(1), countA)
	assert.Equal(t, int64(0), countB, "second DB must not see writes to the first")
}

// withEnv saves+restores an env var around the test.
func withEnv(t *testing.T, key, val string) {
	t.Helper()
	prev, ok := os.LookupEnv(key)
	if val == "" {
		require.NoError(t, os.Unsetenv(key))
	} else {
		require.NoError(t, os.Setenv(key, val))
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
