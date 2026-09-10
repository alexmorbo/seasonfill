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

// TestSharedVerticalsNoInternalImports enforces ADR-0025 R3: the
// vertical-parity registry (internal/shared/verticals) is a pure
// DECLARATION and must import NOTHING from this module.
//
// The rule is deliberately STRICTER than its sibling
// TestSharedClientsNoBackwardsImports: that one bans the four
// horizontal-CA layer roots with a documented allowList, this one bans
// EVERY github.com/alexmorbo/seasonfill/... import with an EMPTY
// allowList. Rationale: the registry describes the codebase. The instant
// it imports any part of that codebase, the description starts depending
// on the thing described — a Gap could be "closed" by a compile-time
// symbol move rather than by real work, and the conformance test would
// stop being an independent witness.
//
// Only the standard library is permitted.
//
// NOTE: this file runs via `make test-lint-rule` only. The CI `lint` job
// runs golangci-lint and never invokes the lint-tagged suite, so the same
// scan is duplicated — on purpose — as
// TestADR0025_F0_VerticalsPackageHasNoInternalImports in
// tests/integration/adr0025_f0_vertical_parity_test.go, which CI does
// execute (./tests/... in INTEGRATION_PKGS_SQLITE and
// ./tests/integration/... in INTEGRATION_PKGS_POSTGRES).
func TestSharedVerticalsNoInternalImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	root := filepath.Join(repoRoot, "internal", "shared", "verticals")
	const modPath = "github.com/alexmorbo/seasonfill"

	fset := token.NewFileSet()
	var offenders []string
	scanned := 0

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		scanned++
		for _, imp := range f.Imports {
			v := strings.Trim(imp.Path.Value, `"`)
			if v == modPath || strings.HasPrefix(v, modPath+"/") {
				rel, _ := filepath.Rel(repoRoot, path)
				offenders = append(offenders,
					rel+": imports "+v+" — the parity registry must import stdlib ONLY (ADR-0025 R3)")
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}
	if scanned == 0 {
		t.Fatalf("no .go files found under %s — the scan silently covered nothing", root)
	}

	for _, o := range offenders {
		t.Errorf("  %s", o)
	}
	if len(offenders) > 0 {
		t.Errorf("internal/shared/verticals has %d internal-import offenders", len(offenders))
	}
}
