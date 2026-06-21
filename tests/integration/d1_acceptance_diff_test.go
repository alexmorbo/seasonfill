//go:build integration

// D-1-8 — atlas-diff regression test (PRD §D-1 bullet #10).
//
// Strategy:
//
//  1. Invoke `atlas migrate diff drop_probe --env postgres` with
//     SEASONFILL_DROP_INDEX=series_tmdb_type_idx in the environment.
//     The loader (infrastructure/database/schema/cmd/loader/main.go)
//     reads that env var and removes the named index from the schema
//     emitted to Atlas's dev-DB.
//  2. Atlas diffs the (now index-less) desired schema against the
//     migration directory and emits a *_drop_probe.up.sql containing
//     `DROP INDEX "series_tmdb_type_idx"`.
//  3. The test asserts the probe SQL file exists and contains the DROP
//     statement, then deletes both .up.sql and .down.sql to keep the
//     migration tree clean. The atlas.sum integrity file is regenerated
//     via `atlas migrate hash` so the tree is restored exactly to the
//     pre-test state.
//
// Skipped when the atlas binary is not on PATH (typical local dev
// environment without `make atlas-install`). CI runs the diff-check job
// which installs atlas first, so the skip path is acceptable locally.
package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestD1_Acceptance_AtlasDiffRegression verifies that atlas detects a
// missing index as a DROP INDEX in the generated diff. This is the
// empirical proof that bullet #10 holds — if a future schema.go edit
// silently drops an index the diff path will catch it.
//
// Requires DOCKER for atlas's dev-DB (postgres container). Atlas spawns
// the dev-DB automatically per its env block (`dev = "docker://..."`).
// CI uses the same path via the migrations-diff-check job.
func TestD1_Acceptance_AtlasDiffRegression(t *testing.T) {
	if _, err := exec.LookPath("atlas"); err != nil {
		t.Skip("atlas binary not on PATH; install via `make atlas-install` to run locally")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	repoRoot := d1RepoRoot(t)
	migDir := filepath.Join(repoRoot, "infrastructure", "database", "migrations", "postgres")

	// Defensive cleanup: even if the test fails mid-flight, never leave
	// the probe migration committed accidentally. atlas.sum is restored
	// from the original tree via `git restore` in the operator workflow
	// — locally we settle for `atlas migrate hash` rewriting it.
	cleanupProbe := func() {
		matches, _ := filepath.Glob(filepath.Join(migDir, "*_drop_probe.up.sql"))
		for _, m := range matches {
			_ = os.Remove(m)
		}
		matchesDown, _ := filepath.Glob(filepath.Join(migDir, "*_drop_probe.down.sql"))
		for _, m := range matchesDown {
			_ = os.Remove(m)
		}
		hashCtx, hashCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer hashCancel()
		hashCmd := exec.CommandContext(hashCtx, "atlas", "migrate", "hash", "--env", "postgres")
		hashCmd.Dir = repoRoot
		_, _ = hashCmd.CombinedOutput()
	}
	t.Cleanup(cleanupProbe)

	cmd := exec.CommandContext(ctx, "atlas", "migrate", "diff", "drop_probe", "--env", "postgres")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "SEASONFILL_DROP_INDEX=series_tmdb_type_idx")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "atlas migrate diff failed: %s", string(out))

	matches, err := filepath.Glob(filepath.Join(migDir, "*_drop_probe.up.sql"))
	require.NoError(t, err)
	require.Lenf(t, matches, 1,
		"expected exactly one *_drop_probe.up.sql, got: %v\natlas output:\n%s",
		matches, string(out))

	body, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	require.Containsf(t, string(body), `DROP INDEX "series_tmdb_type_idx"`,
		"atlas did not detect index drop; probe body:\n%s", string(body))
}

// d1RepoRoot resolves the absolute path to the seasonfill repository
// root by walking two levels up from this test file. Lets the test
// invoke the atlas CLI with `--env postgres`, which reads atlas.hcl
// from the repo root.
func d1RepoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	root := filepath.Join(filepath.Dir(file), "..", "..")
	abs, err := filepath.Abs(root)
	require.NoError(t, err)
	// Sanity: the repo root must contain atlas.hcl.
	_, err = os.Stat(filepath.Join(abs, "atlas.hcl"))
	require.NoErrorf(t, err, "atlas.hcl missing at resolved repo root %s", abs)
	return abs
}
