package commands

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withResetPasswordEnv configures env vars so config.FromEnv +
// database.Open + Migrate succeed against an isolated SQLite file.
func withResetPasswordEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reset_pw.db")
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", dbPath)
	t.Setenv("SEASONFILL_API_KEY", "test-master-key-for-reset-password-cli")
	t.Setenv("SEASONFILL_LOG_LEVEL", "error")
	t.Setenv("SEASONFILL_LOG_FORMAT", "json")
	return dbPath
}

// NOTE: tests that call t.Setenv cannot also t.Parallel — Go's testing
// framework rejects the combination at runtime. The non-Setenv guards
// (--set missing / too-short password) below DO call t.Parallel.

func TestResetPassword_MissingFlag(t *testing.T) {
	t.Parallel()
	err := ResetPassword(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--set")
}

func TestResetPassword_TooShort(t *testing.T) {
	t.Parallel()
	err := ResetPassword([]string{"--set", "short"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chars")
}

// TestResetPassword_NoBootstrappedUser covers the CLI guard before any
// user row exists. Run path mirrors prod: open DB, migrate, attempt
// Get → typed ErrNotFound surfaces the friendly "run the pod once"
// hint.
func TestResetPassword_NoBootstrappedUser(t *testing.T) {
	withResetPasswordEnv(t)
	err := ResetPassword([]string{"--set", "ValidPassword123!"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no admin user")
}

// TestResetPassword_DBUnreachable covers the open-db error surface.
func TestResetPassword_DBUnreachable(t *testing.T) {
	withResetPasswordEnv(t)
	// Point at a path inside a non-existent directory tree so
	// database.Open's MkdirAll fails or fileopen fails. SQLite open
	// requires a writable directory.
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", "/nonexistent/dir/that/cannot/be/created/db.sqlite")
	err := ResetPassword([]string{"--set", "ValidPassword123!"})
	require.Error(t, err)
}
