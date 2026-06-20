//go:build lint

package tests

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestWiringNoBackwardsImports enforces story 452 A-1-26 §3.2: the
// internal/wiring package is the composition-root KERNEL. It owns the
// directed import graph one way:
//
//	cmd/server          → internal/wiring → every internal/<context>
//	internal/wiring     → internal/<any-context>
//	internal/<context>  → MUST NOT import internal/wiring
//
// The check below walks every package under internal/ EXCEPT
// internal/wiring/ itself and asserts no .go file imports the wiring
// package. A backward import would make a bounded context depend on
// its own composition root — turning the kernel relationship into a
// cycle and defeating the per-context split.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestWiringNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	internalRoot := filepath.Join(repoRoot, "internal")
	wiringRoot := filepath.Join(internalRoot, "wiring")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedImport := modPath + "/internal/wiring"

	fset := token.NewFileSet()
	var offenders []string

	walkErr := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip the wiring package itself — it owns its own
			// internal imports and is the test subject's inverse.
			if path == wiringRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Logf("parse %s: %v", path, perr)
			return nil
		}
		for _, imp := range f.Imports {
			v := strings.Trim(imp.Path.Value, `"`)
			if v == bannedImport || strings.HasPrefix(v, bannedImport+"/") {
				rel, _ := filepath.Rel(repoRoot, path)
				offenders = append(offenders, rel+": imports composition-root path "+v+" (internal/<context> packages must not depend on the wiring kernel)")
				return nil
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", internalRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("internal/wiring has %d backward-import offenders — composition-root kernel boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
