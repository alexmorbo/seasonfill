//go:build lint

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUseAnyRejectsInterfaceLiteral verifies that the `.golangci.yml`
// `use-any` rule actually fires on `interface{}`. Without this guard
// the rule can silently drift (typo in name, accidental removal,
// linter upgrade changing semantics) and we'd only notice once new
// `interface{}` had already landed in production code.
//
// Implementation note: the rule is revive's purpose-built `use-any`
// (forbidigo can't match type literals — it operates on
// identifiers/call expressions only).
//
// Build tag: `lint` — opt-in. CI runs this via `make test-lint-rule`.
func TestUseAnyRejectsInterfaceLiteral(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH; skipping use-any regression test")
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
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module useany_check\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	bad := "package badpkg\n\nvar X interface{}\n"
	if err := os.WriteFile(filepath.Join(tmp, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write bad.go: %v", err)
	}
	// Copy the live seasonfill config so the rule under test is the
	// same one that ships.
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
		t.Fatalf("golangci-lint exited 0 on interface{} — use-any rule is not firing.\nOutput:\n%s", out)
	}
	if !strings.Contains(string(out), "use-any") {
		t.Fatalf("expected `use-any` in lint output, got:\n%s", out)
	}
	if !strings.Contains(string(out), "interface{}") && !strings.Contains(string(out), "any") {
		// The configured msg is
		// "since Go 1.18 'interface{}' can be replaced by 'any'" —
		// either token in the diagnostic body is acceptable evidence
		// the rule fired against the right pattern.
		t.Fatalf("expected use-any diagnostic to reference interface{}/any, got:\n%s", out)
	}
}
