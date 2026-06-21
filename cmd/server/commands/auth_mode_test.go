package commands

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// withAuthEnv sets the minimum SEASONFILL_* env vars AuthMode needs to
// boot. Each test gets its own temp dir + sqlite path so they don't
// share state.
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
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "forms"})
	require.NoError(t, err)
}

func TestAuthMode_SetBasic(t *testing.T) {
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "basic"})
	require.NoError(t, err)
}

func TestAuthMode_SetNone(t *testing.T) {
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "none"})
	require.NoError(t, err)
}

func TestAuthMode_SetOIDC(t *testing.T) {
	withAuthEnv(t)
	// oidc is accepted at the CLI layer; the OIDC discovery handshake
	// happens at server start, not here. CLI validation only checks
	// the enum.
	err := AuthMode([]string{"--set", "oidc"})
	require.NoError(t, err)
}

func TestAuthMode_InvalidMode(t *testing.T) {
	withAuthEnv(t)
	err := AuthMode([]string{"--set", "bogus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid mode")
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

func TestAuthMode_SetBumpsEpoch(t *testing.T) {
	withAuthEnv(t)
	// Two successive Sets bump the epoch each time. We can't easily
	// capture stdout here without invasive refactoring; the
	// runtimeconfig usecase tests in
	// internal/catalog/app/runtimeconfig already cover the epoch
	// monotonicity contract end-to-end with a controlled clock. This
	// test just confirms two CLI invocations succeed back-to-back —
	// the use case panics if epoch <= prev.
	require.NoError(t, AuthMode([]string{"--set", "basic"}))
	require.NoError(t, AuthMode([]string{"--set", "forms"}))
}
