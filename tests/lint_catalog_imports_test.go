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

// TestCatalogNoBackwardsImports enforces story 441 A-1-15 §3.3 and
// 442 A-1-16 §3.3: every package under internal/catalog/ MUST NOT
// import the legacy horizontal-CA dirs that the bounded context was
// migrated OUT of. Specifically the old siblings that hosted catalog
// code before the vertical-slice extraction:
//
//   - domain/series        (now internal/catalog/domain/series)
//   - domain/instance      (now internal/catalog/domain/instance)
//   - domain/webhook       (now internal/catalog/domain/webhook)
//   - domain/release       (now internal/catalog/domain/release)
//   - application/instance       (now internal/catalog/app/instance)
//   - application/scan           (now internal/catalog/app/scan)
//   - application/rescan         (now internal/catalog/app/rescan)
//   - application/webhook        (now internal/catalog/app/webhook)
//   - application/webhookinstall (now internal/catalog/app/webhookinstall)
//   - application/torrentsync    (now internal/catalog/app/torrentsync)
//   - application/gc             (now internal/catalog/app/gc)
//   - application/runtimeconfig  (now internal/catalog/app/runtimeconfig)
//
// And the general horizontal layers — internal/catalog/ is a leaf
// context (within the catalog vertical), so it must not reach into
// application/, domain/, infrastructure/, or interface/ at all
// (except via the internal/shared/ kernel and the cross-context
// ports.go contracts).
//
// Scope: production .go files and _test.go files alike. The story
// 441 move was structural; no test should suddenly reach into the
// legacy tree either.
//
// Carve-outs (explicit allowlist):
//
//   - internal/shared/* — kernel imports are always allowed.
//   - application/ports — temporary tolerance because the catch-all
//     ports package still exports cross-context port contracts
//     (Settings, Repository, Reload). Story 449 will split the ports
//     catalog into per-context homes.
//   - infrastructure/database — InstanceUseCase reads/writes the
//     sonarr_instance GORM model directly (cipher + reload bus
//     orchestrator). Same model-split deferral as A-1-9 / A-1-10.
//   - internal/runtime — InstanceUseCase reads the active runtime
//     snapshot + crypto cipher for envelope (de)encryption.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestCatalogNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "catalog")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/domain/series",
		modPath + "/domain/instance",
		modPath + "/domain/webhook",
		modPath + "/domain/release",
		modPath + "/application/instance",
		modPath + "/application/scan",
		modPath + "/application/rescan",
		modPath + "/application/webhook",
		modPath + "/application/webhookinstall",
		modPath + "/application/torrentsync",
		modPath + "/application/gc",
		modPath + "/application/runtimeconfig",
	}
	// Banned at the layer level: any path under these roots that
	// isn't in the carve-out below is a regression.
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	// Carve-outs — see godoc above for rationale.
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/infrastructure/database",
		modPath + "/internal/runtime",
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

			// Hard-banned exact-prefix matches (the old catalog
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
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (catalog must use internal/shared/ or its own subtree)")
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
		t.Errorf("catalog has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
