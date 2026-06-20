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

// TestDiscoveryNoBackwardsImports enforces story 447 A-1-21 §3.3:
// every package under internal/discovery/ MUST NOT import the
// horizontal-CA dirs (application/, domain/, infrastructure/,
// interface/) at all. The discovery bounded context is a Phase 1
// skeleton — its leaves currently contain only doc.go reservations
// for the Phase 3 N-2 feature (PRD §5.1). The guard pins the
// vertical-slice boundary BEFORE any feature code lands so a casual
// edit cannot accidentally reach back into the legacy tree while
// the leaves are empty (which would silently grandfather a violation
// the operator would only discover during Phase 3).
//
// Scope: every .go file (production + _test.go) under
// internal/discovery/.
//
// Carve-outs (allowlist) — INTENTIONALLY EMPTY at story 447. Phase 3
// N-2 will add the carve-outs it actually needs (e.g.
// application/ports until the discovery port surface lands locally,
// interface/http/dto for the shared HTTP error envelope) at the
// moment those imports are introduced. The empty allowlist is the
// signal that this context has nothing yet, not an oversight.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestDiscoveryNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "discovery")

	modPath := "github.com/alexmorbo/seasonfill"
	// Banned at the layer level: any path under these roots is a
	// regression. No allowlist until Phase 3 N-2 deliberately opens
	// the first carve-out.
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}

	fset := token.NewFileSet()
	var offenders []string

	walkErr := filepath.WalkDir(ctxRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
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
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (discovery is a Phase 1 skeleton; add an explicit carve-out in lint_discovery_imports_test.go when Phase 3 N-2 needs this import)")
					break
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", ctxRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("discovery has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
