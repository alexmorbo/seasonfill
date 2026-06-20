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

// TestAdminNoBackwardsImports enforces story 428 A-1-2 §3.3: every
// package under internal/admin/ MUST NOT import the legacy horizontal-
// CA dirs that the bounded context was migrated OUT of. Specifically
// the four old siblings that hosted admin code before the vertical-
// slice extraction:
//
//   - application/auth          (now internal/admin/app)
//   - domain/admin              (now internal/admin/domain)
//   - infrastructure/oidc       (now internal/admin/infrastructure/oidc)
//   - infrastructure/ratelimit  (now internal/admin/infrastructure/ratelimit)
//
// And the general horizontal layers — internal/admin/ is a leaf
// context, so it must not reach into application/, domain/,
// infrastructure/, or interface/ at all (except via the
// internal/shared/ kernel and the cross-context ports.go contracts).
//
// Scope: production .go files and _test.go files alike. The story 428
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowlist):
//
//   - internal/shared/* — kernel imports are always allowed.
//   - application/ports — temporary tolerance because the catch-all
//     ports package still exports AdminUserRepository and the
//     cross-context port set. A later pass (after the admin port
//     surface lands in internal/admin/app/ports.go) will relocate
//     these into the admin context. Until then, internal/admin/ MAY
//     import application/ports/{...}.
//   - infrastructure/database — TEMP tolerance for the shared GORM
//     model types (AdminUserModel, AppSettingsModel, QuotaStateModel)
//     that the admin persistence repos still reference. The repos
//     themselves were moved into internal/admin/persistence by story
//     429; story 449 (model split) will relocate the model structs
//     into per-context packages and drop this carve-out.
//   - interface/http/dto — shared HTTP error/envelope DTO used by
//     every handler. Will likely relocate to internal/shared/dto/ in
//     a later pass; for now any admin rest code (story 430) imports
//     it like every other rest package.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestAdminNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "admin")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/auth",
		modPath + "/domain/admin",
		modPath + "/infrastructure/oidc",
		modPath + "/infrastructure/ratelimit",
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

			// Hard-banned exact-prefix matches (the old admin
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
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (admin must use internal/shared/ or its own subtree)")
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
		t.Errorf("admin has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}

// TestAdminPersistenceNoBackwardsImports is the focused sub-guard for
// story 429 A-1-3: the newly extracted internal/admin/persistence/
// leaf MUST NOT reach back into the catalog half of the old shared
// infrastructure/database/repositories/ directory. Any reference from
// admin persistence to a catalog repo (e.g. SonarrInstanceRepository,
// GrabRepository, etc.) is a vertical-slice boundary breach that the
// general admin guard above would also catch via the layer-level ban,
// but this dedicated check pins the regression message to the exact
// rule that drove story 429 — making future violations self-document
// in the test output without forcing operators to grep PRD §3.2.
//
// Scope: every .go file under internal/admin/persistence (production
// + _test.go). Banned: the catalog repository package and any non-
// admin sibling under infrastructure/database/repositories/.
//
// Allowed (carve-outs): infrastructure/database (shared GORM models,
// see TestAdminNoBackwardsImports godoc for the story 449 take-up
// note).
func TestAdminPersistenceNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "admin", "persistence")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPath := modPath + "/infrastructure/database/repositories"

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
			if v == bannedPath || strings.HasPrefix(v, bannedPath+"/") {
				rel, _ := filepath.Rel(repoRoot, path)
				offenders = append(offenders, rel+": imports legacy catalog repo path "+v+" (admin persistence is a leaf; use internal/admin/ or internal/shared/ instead)")
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", ctxRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("admin/persistence has %d backward-import offenders — story 429 boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
