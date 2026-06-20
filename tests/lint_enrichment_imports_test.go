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

// TestEnrichmentNoBackwardsImports enforces story 436 A-1-10 §3.3:
// every package under internal/enrichment/ MUST NOT import the legacy
// horizontal-CA dirs that the bounded context was migrated OUT of.
// Specifically the six old siblings that hosted enrichment code
// before the vertical-slice extraction:
//
//   - application/enrichment         (now internal/enrichment/app)
//   - application/people             (now internal/enrichment/app/people)
//   - application/externalservices   (now internal/enrichment/app/externalservices)
//   - domain/enrichment              (now internal/enrichment/domain/enrichment)
//   - domain/people                  (now internal/enrichment/domain/people)
//   - domain/taxonomy                (now internal/enrichment/domain/taxonomy)
//
// And the general horizontal layers — internal/enrichment/ is a leaf
// context, so it must not reach into application/, domain/,
// infrastructure/, or interface/ at all (except via the
// internal/shared/ kernel and the cross-context ports.go contracts).
//
// Scope: production .go files and _test.go files alike. The story 436
// move was structural; no test should suddenly reach into the legacy
// tree either. Story 437 (A-1-11) extended the bounded context with
// internal/enrichment/persistence (catalog-data + people + taxonomy +
// i18n repositories migrated out of infrastructure/database/
// repositories). Persistence files are covered by this same depcheck
// — they may import infrastructure/database (for GORM models) but
// MUST NOT reach back into infrastructure/database/repositories for
// neighbour repos, application/*, domain/*, or interface/*.
//
// Carve-outs (explicit allowlist):
//
//   - internal/shared/* — kernel imports are always allowed.
//   - application/ports — temporary tolerance because catch-all ports
//     still exports cross-context port contracts (series, sonarr,
//     repository). Story 449 will split the ports catalog into
//     per-context homes.
//   - domain/series — series.Canon + hydration helpers used by the
//     series_worker patch flow. Will relocate when story 446 splits
//     the series domain into its own vertical slice.
//   - domain/instance, domain/release, domain/webhook — sibling
//     domain value types referenced by the dispatcher (release tag
//     lookup), externalservices test_runner (instance config), and
//     shared error sentinels. Relocate when their respective vertical
//     slices land.
//   - infrastructure/database — externalservices.Settings UseCase
//     reads/writes the external_services + quota_state GORM model
//     types (ExternalServicesModel, QuotaStateModel). Same model-
//     split deferral as A-1-9.
//   - infrastructure/sonarr — series_worker pulls Sonarr error
//     sentinels for the 404-as-degraded mapping. Will relocate when
//     story 447 extracts the sonarr_sync bounded context.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestEnrichmentNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "enrichment")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/enrichment",
		modPath + "/application/people",
		modPath + "/application/externalservices",
		modPath + "/domain/enrichment",
		modPath + "/domain/people",
		modPath + "/domain/taxonomy",
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
		modPath + "/domain/series",
		modPath + "/domain/instance",
		modPath + "/domain/release",
		modPath + "/domain/webhook",
		modPath + "/infrastructure/database",
		modPath + "/infrastructure/sonarr",
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

			// Hard-banned exact-prefix matches (the old enrichment
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
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (enrichment must use internal/shared/ or its own subtree)")
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
		t.Errorf("enrichment has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
