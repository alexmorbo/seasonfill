package commands

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// withAuthEnv sets the minimum SEASONFILL_* env vars AuthMode needs to
// boot. 466a's `--get` path doesn't open the DB but the env vars are
// kept for 466b's `--set` path to inherit.
func withAuthEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", filepath.Join(dir, "auth_mode.db"))
	t.Setenv("SEASONFILL_API_KEY", "test-master-key-for-auth-mode-cli")
	t.Setenv("SEASONFILL_LOG_LEVEL", "error")
	t.Setenv("SEASONFILL_LOG_FORMAT", "json")
}

// NOTE: tests that call t.Setenv cannot also t.Parallel — Go's testing
// framework rejects the combination at runtime.

func TestAuthMode_GetReturnsForms(t *testing.T) {
	withAuthEnv(t)
	err := AuthMode([]string{"--get"})
	require.NoError(t, err)
}

func TestAuthMode_SetForms(t *testing.T) {
	t.Parallel()
	// 466a: --set path stays disabled pending 466b runtime_config rewrite.
	err := AuthMode([]string{"--set", "forms"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "466b")
}

func TestAuthMode_SetBasic(t *testing.T) {
	t.Parallel()
	err := AuthMode([]string{"--set", "basic"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "466b")
}

func TestAuthMode_SetNone(t *testing.T) {
	t.Parallel()
	err := AuthMode([]string{"--set", "none"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "466b")
}

func TestAuthMode_InvalidMode(t *testing.T) {
	t.Parallel()
	// Validation pending 466b — any --set value lands on the
	// "pending 466b" placeholder for now.
	err := AuthMode([]string{"--set", "oidc"})
	require.Error(t, err)
}

func TestAuthMode_NoArgs(t *testing.T) {
	t.Parallel()
	err := AuthMode([]string{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--get or --set")
}

func TestAuthMode_BothArgs(t *testing.T) {
	t.Parallel()
	err := AuthMode([]string{"--get", "--set", "forms"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}

func TestAuthMode_DBUnreachable(t *testing.T) {
	t.Parallel()
	// 466a: --get does NOT touch the DB (returns runtime.Defaults());
	// the DB-unreachable scenario is restored in 466b when the --set
	// path opens the runtime_config repo.
	t.Skip("pending 466b — auth-mode --set DB path stubbed (D2-revised-roadmap.md)")
}

func TestAuthMode_SetBumpsEpoch(t *testing.T) {
	t.Parallel()
	// 466a: SessionEpoch bump lives on the --set path, restored in
	// 466b alongside the runtime_config rewrite.
	t.Skip("pending 466b — auth-mode --set epoch bump (D2-revised-roadmap.md)")
}
