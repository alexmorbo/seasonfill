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

// TestSharedReloadSchedulerNoBackwardsImports enforces story 440 A-1-14
// §3.2: the reload fan-out bus + scheduler façade live in
// internal/shared/reload/ and internal/shared/scheduler/ as part of
// the shared kernel (PRD §3.2). The bus subscribes loops + caches to
// config-change events; the scheduler wraps robfig/cron. Both are
// imported by internal/wiring/, commands/, every loop, and several
// caches — so they sit ABOVE every vertical context and BELOW only the
// other kernel packages (internal/shared/*).
//
// Like every kernel package, reload + scheduler MUST NOT import
// horizontal-CA layers (application/, domain/, infrastructure/,
// interface/) except via the explicit allowList carve-outs documented
// below, and MUST NOT import vertical-slice contexts (internal/<ctx>/)
// other than internal/shared/.
//
// Scope: production .go files and _test.go files alike. The story 440
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowList) — horizontal-CA layers:
//
//   - application/ports — reload subscribers reference application
//     ports (e.g. SettingsRepository, AuthRuntime) wired by the loops
//     they swap. Will relocate when story 449 splits the ports catalog
//     into per-context homes.
//   - application/scan — scheduler_subscriber wires the ScanUC into
//     the cron Boot func at reload-time; the cron façade only knows
//     the BootScheduler signature, so the reload subscriber owns the
//     concrete bind. Same story 449 deferral.
//   - domain/release, domain/series — sonarr_clients_subscriber +
//     fake_sonarr_test reference the canon release / series domain
//     value objects exposed by the Sonarr kernel client. Same model-
//     split deferral as the shared/clients allowList.
//   - interface/http/middleware — auth_middleware_subscriber Stores a
//     refreshed AuthRuntime into the Gin middleware pointer at reload
//     time. The pointer's concrete type is owned by the HTTP iface;
//     the reload subscriber holds it as an interface in the kernel.
//
// Carve-outs (explicit allowList) — vertical-slice contexts:
//
//   - internal/admin/infrastructure/ratelimit — global_rate_limiter_
//     subscriber Stores a refreshed *ratelimit.Limiter (admin's
//     concrete) into the global limiter pointer. The pointer's
//     concrete type is owned by admin; the kernel subscriber holds it
//     as an opaque pointer/value. Will fold into shared/ratelimit/ in
//     a later kernel pass.
//   - internal/admin/infrastructure/oidc — oidc_provider_subscriber
//     Stores a refreshed *oidc.Provider (admin's concrete) into the
//     OIDC cache pointer. Same admin-concrete carve-out shape as the
//     ratelimit one above; will fold into shared/oidc/ later.
//   - internal/grab/app/evaluate — sonarr_clients_subscriber rebinds
//     the grab evaluator's cached sonarr clients map on reload. The
//     bus-side dependency is the evaluator's swap surface, not its
//     decision logic; will relocate behind a swap port in a later pass.
//   - internal/runtime — internal/runtime is itself a kernel-adjacent
//     bootstrap that the reload bus wraps; the subscriber re-publishes
//     the runtime snapshot. Treated as kernel-equivalent until story
//     449 elevates internal/runtime/ into internal/shared/runtime/.
//   - internal/observability — metrics emission is universal-kernel;
//     every shared package may emit metrics. (No carve-out needed —
//     internal/shared/ is implicitly allowed below, but observability
//     lives outside it pending its own kernel move.)
//
// Run via: `make test-lint-rule` (lint build tag).
func TestSharedReloadSchedulerNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	roots := []string{
		filepath.Join(repoRoot, "internal", "shared", "reload"),
		filepath.Join(repoRoot, "internal", "shared", "scheduler"),
	}

	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	// Vertical-slice contexts (internal/<ctx>/) are banned by default;
	// the kernel must not depend on a single context. internal/shared/
	// is always allowed (kernel sibling). The carve-outs below permit
	// the three reload-bus swap surfaces (admin ratelimit, grab
	// evaluator, internal/runtime + observability).
	bannedVerticalSlicePrefixes := []string{
		modPath + "/internal/admin/",
		modPath + "/internal/enrichment/",
		modPath + "/internal/grab/",
		modPath + "/internal/mediaproxy/",
		modPath + "/internal/scan/",
		modPath + "/internal/seriesdetail/",
		modPath + "/internal/watchdog/",
		modPath + "/internal/webhook/",
	}
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/application/scan",
		modPath + "/internal/catalog/domain/release",
		modPath + "/internal/catalog/domain/series",
		modPath + "/interface/http/middleware",
		modPath + "/internal/admin/infrastructure/oidc",
		modPath + "/internal/admin/infrastructure/ratelimit",
		modPath + "/internal/grab/app/evaluate",
		modPath + "/internal/observability",
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
					offenders = append(offenders, rel+": imports horizontal-CA or vertical-slice path "+v+" (shared/reload+scheduler kernel must stay inside its allowList)")
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", ctxRoot, walkErr)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("shared/reload+scheduler has %d backward-import offenders — story 440 A-1-14 kernel boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
