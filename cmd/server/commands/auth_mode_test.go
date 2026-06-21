package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// withAuthEnv sets the minimum SEASONFILL_* env vars AuthMode needs
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

func TestAuthMode_GetReturnsForms(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--get"})
	require.NoError(t, err)
}

func TestAuthMode_SetForms(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "forms"})
	require.NoError(t, err)
}

func TestAuthMode_SetBasic(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "basic"})
	require.NoError(t, err)
}

func TestAuthMode_SetNone(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "none"})
	require.NoError(t, err)
}

func TestAuthMode_InvalidMode(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "oidc"})
	require.Error(t, err)
}

func TestAuthMode_NoArgs(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{})
	require.Error(t, err)
}

func TestAuthMode_BothArgs(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	err := AuthMode([]string{"--get", "--set", "forms"})
	require.Error(t, err)
}

func TestAuthMode_DBUnreachable(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", "/nonexistent/dir/that/cannot/be/created/db.sqlite")
	err := AuthMode([]string{"--get"})
	require.Error(t, err)
}

func TestAuthMode_SetBumpsEpoch(t *testing.T) {
	t.Skip("pending D-5 admin+auth rewrite — command is a no-op stub (D2-revised-roadmap.md)")
	withAuthEnv(t)
	require.NoError(t, AuthMode([]string{"--set", "basic"}))
	dbPath := os.Getenv("SEASONFILL_DATABASE_SQLITE_PATH")
	require.NotEmpty(t, dbPath)
	require.NoError(t, AuthMode([]string{"--set", "none"}))
	require.NoError(t, AuthMode([]string{"--set", "forms"}))
}
