package commands

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// withReparseEnv mirrors withAuthEnv — each invocation gets its own
// temp sqlite path. Reparse's full replay loop lands in D-6 (grab
// context rewrite); this smoke test covers the 466b bedrock plumbing
// only.
func withReparseEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", filepath.Join(dir, "reparse.db"))
	t.Setenv("SEASONFILL_API_KEY", "test-master-key-for-reparse-cli")
	t.Setenv("SEASONFILL_LOG_LEVEL", "error")
	t.Setenv("SEASONFILL_LOG_FORMAT", "json")
}

func TestReparse_BootsAndExitsClean_NoInstances(t *testing.T) {
	withReparseEnv(t)
	err := Reparse(context.Background(), nil)
	require.NoError(t, err,
		"reparse must boot cleanly on an empty DB (no instances configured)")
}

func TestReparse_AcceptsEmptyArgs(t *testing.T) {
	withReparseEnv(t)
	err := Reparse(context.Background(), []string{})
	require.NoError(t, err)
}
