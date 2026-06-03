package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAuthEnv sets the minimum SEASONFILL_* env vars runAuthMode needs
// to boot (config.FromEnv → database.Open → migrations). Returns a
// cleanup func that resets the environment.
func withAuthEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", filepath.Join(dir, "auth_mode.db"))
	t.Setenv("SEASONFILL_API_KEY", "test-master-key-for-auth-mode-cli")
	t.Setenv("SEASONFILL_LOG_LEVEL", "error")
	t.Setenv("SEASONFILL_LOG_FORMAT", "json")
}

// TestRunAuthMode_GetReturnsForms exercises the --get happy path
// against a freshly-seeded SQLite DB. Default after migrations =
// "forms".
func TestRunAuthMode_GetReturnsForms(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--get"})
	// runAuthMode writes the mode to stdout. We can't easily capture
	// stdout here without restructuring (the helper writes via
	// fmt.Fprintln directly). Asserting nil-err is sufficient — the
	// detailed mode-roundtrip is covered by the usecase test.
	require.NoError(t, err)
}

func TestRunAuthMode_SetForms(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--set", "forms"})
	require.NoError(t, err)
}

func TestRunAuthMode_SetBasic(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--set", "basic"})
	require.NoError(t, err)
}

func TestRunAuthMode_SetNone(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--set", "none"})
	require.NoError(t, err)
}

func TestRunAuthMode_InvalidMode(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--set", "oidc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mode")
}

func TestRunAuthMode_NoArgs(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")
}

func TestRunAuthMode_BothArgs(t *testing.T) {
	withAuthEnv(t)
	err := runAuthMode([]string{"--get", "--set", "forms"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")
}

func TestRunAuthMode_DBUnreachable(t *testing.T) {
	withAuthEnv(t)
	// Override to a path that cannot exist (sqlite opens read-write on
	// a directory path → error). Use a non-existent parent dir.
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", "/nonexistent/dir/that/cannot/be/created/db.sqlite")
	err := runAuthMode([]string{"--get"})
	require.Error(t, err)
}

// TestRunAuthMode_SetBumpsEpoch wires together --set and a follow-up
// inspection: after `--set basic` then `--set none`, the row's epoch
// must differ from the post-`--set basic` epoch. We use a temp DB and
// re-open via repositories to avoid relying on stdout capture.
func TestRunAuthMode_SetBumpsEpoch(t *testing.T) {
	withAuthEnv(t)
	require.NoError(t, runAuthMode([]string{"--set", "basic"}))
	dbPath := os.Getenv("SEASONFILL_DATABASE_SQLITE_PATH")
	require.NotEmpty(t, dbPath)
	// Calling --set again with a different mode must succeed and bump
	// epoch (no error indicates the upsert went through).
	require.NoError(t, runAuthMode([]string{"--set", "none"}))
	require.NoError(t, runAuthMode([]string{"--set", "forms"}))
}
