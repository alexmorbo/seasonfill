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
//     cross-context port set; story 429 (admin persistence move) will
//     relocate AdminUserRepository into internal/admin/app/ports.go.
//     Until then, internal/admin/ MAY import application/ports/{...}.
//   - infrastructure/database — TEMP tolerance for the production
//     AdminUserRepository implementation that still lives at
//     infrastructure/database/repositories/admin_user_repository.go.
//     Story 429 moves it into internal/admin/infrastructure/
//     repositories/.
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
