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

// TestSeriesDetailNoBackwardsImports enforces story 445 A-1-19 §3.3:
// every package under internal/seriesdetail/ MUST NOT import the
// legacy horizontal-CA dir that the bounded context was migrated OUT
// of. Specifically the old sibling that hosted seriesdetail code
// before the vertical-slice extraction:
//
//   - application/seriesdetail (now internal/seriesdetail/app)
//
// And the general horizontal layers — internal/seriesdetail/ is a
// leaf context (within the seriesdetail vertical), so it must not
// reach into application/, domain/, infrastructure/, or interface/
// at all (except via the internal/shared/ kernel, the cross-context
// ports.go contracts, and the carve-outs below).
//
// Scope: production .go files and _test.go files alike. The story
// 445 move was structural; no test should suddenly reach into the
// legacy tree either.
//
// Carve-outs (explicit allowlist):
//
//   - internal/shared/* — kernel imports are always allowed.
//   - internal/catalog/domain/* — sibling vertical context domain
//     types consumed by value (series.Canon, ...). Catalog owns the
//     canonical projection types the composer fans in.
//   - internal/enrichment/domain/* — sibling vertical context domain
//     types consumed by value (enrichment.Series, people.Person,
//     taxonomy.Genre, ...). Enrichment owns the third-party metadata
//     value types the composer fans in.
//   - application/ports — temporary tolerance because the catch-all
//     ports package still exports cross-context port contracts. Story
//     449 will split the ports catalog into per-context homes.
//   - infrastructure/database — Composer/MediaResolver read the
//     GORM-generated row types directly (model carve-out). Same
//     deferral as the catalog/grab/watchdog vertical slices.
//   - infrastructure/database/repositories — Composer's narrow ports
//     declare repository constructor types; the carve-out matches
//     application/ports — story 449+ extracts these.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestSeriesDetailNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "seriesdetail")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/seriesdetail",
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
	}
	// Cross-context sibling-domain reads (read by value only).
	allowedInternalDomains := []string{
		modPath + "/internal/catalog/domain/",
		modPath + "/internal/enrichment/domain/",
	}

	isAllowed := func(imp string) bool {
		for _, a := range allowList {
			if imp == a || strings.HasPrefix(imp, a+"/") {
				return true
			}
		}
		return false
	}

	isAllowedInternal := func(imp string) bool {
		for _, a := range allowedInternalDomains {
			if strings.HasPrefix(imp, a) {
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

			// Hard-banned exact-prefix matches (the old seriesdetail
			// sibling path). Always a regression.
			for _, b := range bannedPrefixes {
				if v == b || strings.HasPrefix(v, b+"/") {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports banned legacy path "+v)
					return nil
				}
			}

			// Cross-context sibling-domain reads are always OK.
			if isAllowedInternal(v) {
				continue
			}

			// Layer-level ban with allowlist carve-out.
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) && !isAllowed(v) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (seriesdetail must use internal/shared/, sibling internal/*/domain, or its own subtree)")
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
		t.Errorf("seriesdetail has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
