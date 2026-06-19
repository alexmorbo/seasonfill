//go:build lint

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestModernizeRejectsLegacyPatterns verifies that the `.golangci.yml`
// modernize block actually fires on legacy patterns. Without this guard
// the analyzer set can silently drift (typo in checks list, accidental
// removal of the linter, golangci-lint upgrade changing modernize
// bundling) and we'd notice only when new legacy code landed.
//
// Story 417 (F-1 follow-up, task #593).
//
// Build tag: `lint` — opt-in via `make test-lint-rule`.
func TestModernizeRejectsLegacyPatterns(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping modernize regression test")
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	cfg := filepath.Join(repoRoot, ".golangci.yml")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatalf("golangci config not found at %s: %v", cfg, err)
	}

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module modernize_check\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	// Fixture exercises the forvar analyzer — fires reliably on
	// standalone modules (some modernize analyzers like minmax need
	// richer ssa/typeinfo than a bare temp module provides). forvar
	// catches `x := x` loopvar shadowing, redundant since Go 1.22.
	bad := `package badpkg

func F() {
	xs := []int{1, 2, 3}
	for _, x := range xs {
		x := x
		_ = x
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write bad.go: %v", err)
	}
	cfgBytes, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read live golangci config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".golangci.yml"), cfgBytes, 0o644); err != nil {
		t.Fatalf("write tmp golangci config: %v", err)
	}

	cmd := exec.Command("golangci-lint", "run", "./...")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("golangci-lint exited 0 on legacy forvar pattern — modernize rule is not firing.\nOutput:\n%s", out)
	}
	if !strings.Contains(string(out), "modernize") {
		t.Fatalf("expected `modernize` in lint output, got:\n%s", out)
	}
	if !strings.Contains(string(out), "forvar") {
		t.Fatalf("expected `forvar` analyzer to fire on `x := x` loopvar shadowing, got:\n%s", out)
	}
}
