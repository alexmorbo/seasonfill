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

// TestSharedClientsNoBackwardsImports enforces story 435 A-1-9 §3.3:
// every package under internal/shared/clients/ and internal/shared/http/
// is part of the shared kernel (PRD §3.2) — TMDB, OMDb, and the
// externalservices HTTP primitive + Settings UseCase are imported by
// enrichment, mediaproxy, and seriesdetail without crossing context
// boundaries. The kernel layer MUST NOT import application/, domain/,
// infrastructure/, or interface/ (except via the explicit allowList
// carve-outs documented below and internal/shared/ which is always
// allowed).
//
// Scope: production .go files and _test.go files alike. The story 435
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowlist):
//
//   - domain/people, domain/series, domain/taxonomy — TMDB mappers
//     return canon domain value objects (cast / credit / genre /
//     network). These remain horizontal-CA domain packages until a
//     later model-split pass relocates them into internal/shared/
//     domain/; until then the kernel mappers reference them by their
//     current paths.
//   - application/ports — externalservices.Settings UseCase satisfies
//     the catch-all ExternalServicesRepository port surface defined
//     in application/ports. Will relocate when story 449 splits the
//     ports catalog into per-context homes.
//   - infrastructure/database — externalservices.Settings UseCase
//     reads/writes the external_services + quota_state GORM model
//     types (ExternalServicesModel, QuotaStateModel). Same model-
//     split deferral as above.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestSharedClientsNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	roots := []string{
		filepath.Join(repoRoot, "internal", "shared", "clients"),
		filepath.Join(repoRoot, "internal", "shared", "http"),
	}

	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/domain/people",
		modPath + "/domain/series",
		modPath + "/domain/taxonomy",
		modPath + "/infrastructure/database",
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

	for _, ctxRoot := range roots {
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
					if strings.HasPrefix(v, lr) && !isAllowed(v) {
						rel, _ := filepath.Rel(repoRoot, path)
						offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (shared/clients kernel must stay inside its allowList)")
						break
					}
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", ctxRoot, walkErr)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("shared/clients has %d backward-import offenders — story 435 A-1-9 kernel boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
