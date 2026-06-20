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

// TestSharedHTTPNoBackwardsImports enforces story 450 A-1-24 §3.2: the
// HTTP kernel — JSON DTO envelopes + cross-cutting middleware (auth,
// CORS, logging, request-id, validator) — lives in
// internal/shared/http/{dto,middleware}/ as part of the shared kernel
// (PRD §3.2). The DTO sub-package is consumed by every vertical
// context's REST handlers; the middleware sub-package is mounted by
// the composition root and is allowed to reach into a small set of
// kernel-adjacent runtime packages plus the admin/app auth port.
//
// internal/shared/http/edge/ (the server.go wirer + swag doc.go) is
// the HTTP composition root and intentionally imports every vertical
// REST package — it is therefore EXCLUDED from this lint by walking
// only the dto/ and middleware/ subtrees.
//
// internal/shared/http/httpx/ (outbound HTTP plumbing — MetricsTransport,
// TMDB CDN/endpoint detectors) is part of the same shared kernel root
// but its existing boundary is enforced separately by its own absence
// of vertical imports; today it has none, so this lint also covers it
// by walking the dto/ and middleware/ subtrees that are at greater
// risk of regression.
//
// Scope: production .go files and _test.go files alike. The story 450
// move was structural; no test should reach into the legacy
// interface/http tree either (that path no longer exists for the
// moved subtrees).
//
// Carve-outs (explicit allowList) — kernel-adjacent + documented debt:
//
//   - internal/config — read by middleware indirectly via runtime
//     bootstrap helpers.
//   - internal/logger — slog-based structured logger used by every
//     kernel package that emits logs.
//   - internal/runtime + internal/runtime/crypto — runtime bootstrap
//     state (auth mode pointer, cookie key crypto). Kernel-adjacent;
//     no reverse dependencies.
//   - application/ports — current auth middleware reads the
//     AdminSettingsRepo port + SessionStore port off application/ports.
//     This is acknowledged horizontal-CA debt and is tracked for a
//     future cleanup story (move the auth ports into
//     internal/shared/ports or internal/admin/app). The lint encodes
//     today's reality so further regressions can't expand the surface.
//   - internal/admin/app + internal/admin/domain — middleware/auth.go
//     reaches into the admin context for AuthRuntime + AdminUser
//     domain types. Same acknowledged debt as application/ports —
//     auth is the cross-cutting concern that hasn't been carved out
//     yet. Future story can move AuthRuntime into internal/shared/.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestSharedHTTPNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	modPath := "github.com/alexmorbo/seasonfill"

	// Walk dto/ and middleware/ — the kernel-pure layers. edge/ is
	// the composition root and is excluded by design.
	ctxRoots := []string{
		filepath.Join(repoRoot, "internal", "shared", "http", "dto"),
		filepath.Join(repoRoot, "internal", "shared", "http", "middleware"),
	}

	bannedLayerRoots := []string{
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	// Vertical-slice contexts (internal/<ctx>/) are banned by default;
	// the kernel must not depend on a single context. internal/shared/
	// is always allowed (kernel sibling).
	bannedVerticalSlicePrefixes := []string{
		modPath + "/internal/catalog/",
		modPath + "/internal/discovery/",
		modPath + "/internal/enrichment/",
		modPath + "/internal/grab/",
		modPath + "/internal/mediaproxy/",
		modPath + "/internal/scan/",
		modPath + "/internal/seriesdetail/",
		modPath + "/internal/watchdog/",
		modPath + "/internal/webhook/",
	}
	allowList := []string{
		modPath + "/internal/config",
		modPath + "/internal/logger",
		modPath + "/internal/runtime",
		modPath + "/internal/runtime/crypto",
		// Documented debt — middleware/auth.go reaches across.
		modPath + "/application/ports",
		modPath + "/internal/admin/app",
		modPath + "/internal/admin/domain",
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

	for _, ctxRoot := range ctxRoots {
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
				if isAllowed(v) {
					continue
				}
				bannedHit := false
				for _, lr := range bannedLayerRoots {
					if strings.HasPrefix(v, lr) {
						bannedHit = true
						break
					}
				}
				if !bannedHit {
					for _, vp := range bannedVerticalSlicePrefixes {
						if strings.HasPrefix(v, vp) {
							bannedHit = true
							break
						}
					}
				}
				if bannedHit {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA or vertical-slice path "+v+" (shared/http kernel must stay inside its allowList)")
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", ctxRoot, walkErr)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("shared/http has %d backward-import offenders — story 450 A-1-24 kernel boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
