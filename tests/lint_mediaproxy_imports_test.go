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

// TestMediaProxyNoBackwardsImports enforces story 427 A-1-1 §3.3: every
// package under internal/mediaproxy/ MUST NOT import the legacy
// horizontal-CA dirs that the bounded context was migrated OUT of.
// Specifically the four old siblings that hosted media-proxy code
// before the vertical-slice extraction:
//
//   - application/media       (now internal/mediaproxy/app)
//   - domain/media            (now internal/mediaproxy/domain)
//   - infrastructure/mediastore (now internal/mediaproxy/infrastructure)
//   - interface/http/handlers/media* (now internal/mediaproxy/rest/media*)
//
// And the general horizontal layers — internal/mediaproxy/ is a leaf
// context, so it must not reach into application/, domain/,
// infrastructure/, or interface/ at all (except via the
// internal/shared/ kernel and the cross-context ports.go contracts).
//
// Scope: production .go files and _test.go files alike. The story 427
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowlist):
//
//   - internal/shared/* — kernel imports are always allowed.
//   - application/ports — temporary tolerance because catch-all ports
//     still exports the cross-context port set; story 428+ may relocate
//     them to internal/shared/ports. Until then, mediaproxy MAY import
//     application/ports/{repository,sonarr,counters,pagination,...}.
//   - internal/shared/db — TEMP tolerance for the dual-backend
//     test helper used by media_assets repo tests. Story 443 (catalog
//     extraction) will own this path.
//   - interface/http/dto — shared HTTP error/envelope DTO used by every
//     handler. Will likely relocate to internal/shared/dto/ in a later
//     pass; for now mediaproxy/rest imports it like every other rest
//     package.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestMediaProxyNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "mediaproxy")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/media",
		modPath + "/domain/media",
		modPath + "/infrastructure/mediastore",
		modPath + "/interface/http/handlers/media",
	}
	// Banned at the layer level: any path under these roots that isn't
	// in the carve-out below is a regression.
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	// Carve-outs — see godoc above for rationale.
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/internal/shared/db",
		modPath + "/infrastructure/database",
		modPath + "/interface/http/dto",
	}

	isAllowed := func(imp string) bool {
		for _, a := range allowList {
			if imp == a || strings.HasPrefix(imp, a+"/") {
				return true
			}
		}
		return false
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

			// Hard-banned exact-prefix matches (the old media-proxy
			// sibling paths). Always a regression.
			for _, b := range bannedPrefixes {
				if v == b || strings.HasPrefix(v, b+"/") {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports banned legacy path "+v)
					return nil
				}
			}

			// Layer-level ban with allowlist carve-out.
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) && !isAllowed(v) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (mediaproxy must use internal/shared/ or its own subtree)")
					return nil
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", ctxRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("mediaproxy has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
