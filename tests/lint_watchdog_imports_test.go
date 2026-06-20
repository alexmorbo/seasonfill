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

// TestWatchdogNoBackwardsImports enforces story 433 A-1-7 §3.3:
// every package under internal/watchdog/ MUST NOT import the legacy
// horizontal-CA dirs that the bounded context was migrated OUT of.
// Specifically the three old siblings that hosted watchdog code
// before the vertical-slice extraction:
//
//   - application/regrab     (now internal/watchdog/app/regrab)
//   - domain/regrab          (now internal/watchdog/domain/regrab)
//   - domain/cooldown        (now internal/watchdog/domain/cooldown)
//
// And the general horizontal layers — internal/watchdog/ is a leaf
// context, so it must not reach into application/, domain/,
// infrastructure/, or interface/ at all (except via the explicit
// allowList carve-outs documented below and the shared internal/
// kernels that are always allowed).
//
// Scope: production .go files and _test.go files alike. The story
// 433 move was structural; no test should suddenly reach into the
// legacy tree either.
//
// Carve-outs (explicit allowlist):
//
//   - application/ports — until story 449 (model split) relocates
//     GrabRepository / CooldownRepository / WatchdogBlacklistRepository
//     port decls into per-context homes, the regrab UseCase reads
//     them from the catalog ports.go.
//   - application/scan — UseCase consumes scan.Instance (the per-
//     instance scope object the watchdog loops drive) via the
//     handler InstanceRegistry shape.
//   - domain/release + domain/series — read-only cross-context
//     value-object types consumed by the regrab decision pipeline
//     (release rank, season Series id).
//   - infrastructure/qbit + infrastructure/sonarr — UseCase + qbit
//     factory adapter shape; the production wiring stays horizontal
//     until 434/435 land.
//   - internal/grab/{app,domain,domain/decision,app/evaluate} — the
//     watchdog regrab loop hands one Decision off to the grab
//     UseCase (cross-context contract, kernel-shaped).
//   - internal/config + internal/logger + internal/observability +
//     internal/runtime/crypto — kernel imports are always allowed.
//   - internal/shared/* — kernel imports are always allowed.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestWatchdogNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "watchdog")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/regrab",
		modPath + "/domain/regrab",
		modPath + "/domain/cooldown",
	}
	// Banned at the layer level: any path under these roots that isn't
	// in the carve-out below is a regression.
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	allowList := []string{
		modPath + "/application/errtext",
		modPath + "/application/ports",
		modPath + "/application/rescan",
		modPath + "/application/scan",
		modPath + "/domain",
		modPath + "/domain/instance",
		modPath + "/domain/release",
		modPath + "/domain/series",
		modPath + "/infrastructure/database",
		modPath + "/infrastructure/qbit",
		modPath + "/infrastructure/sonarr",
		modPath + "/interface/http/dto",
		modPath + "/interface/http/handlers",
		modPath + "/interface/http/middleware",
		modPath + "/internal/config",
		modPath + "/internal/logger",
		modPath + "/internal/observability",
		modPath + "/internal/runtime/crypto",
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

			// Hard-banned exact-prefix matches (the old watchdog
			// sibling paths). Always a regression.
			for _, b := range bannedPrefixes {
				if v == b || strings.HasPrefix(v, b+"/") {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports banned legacy path "+v)
					return nil
				}
			}

			// Layer-level ban with allowList carve-out.
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) && !isAllowed(v) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (watchdog must use internal/shared/ or its own subtree)")
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
		t.Errorf("watchdog has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}

// TestWatchdogDomainRegrabNoBackwardsImports pins the story 433
// A-1-7 move for internal/watchdog/domain/regrab/ (folded from
// domain/regrab/). The blacklist + counter domain types now live
// inside the watchdog vertical slice and MUST NOT import any of the
// horizontal-CA layers — the domain layer is the cleanest leaf in
// the watchdog subtree and should only depend on internal/shared/ at
// most.
func TestWatchdogDomainRegrabNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "watchdog", "domain", "regrab")
	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	checkWatchdogLeafImports(t, ctxRoot, "watchdog/domain/regrab", bannedLayerRoots)
}

// TestWatchdogDomainCooldownNoBackwardsImports pins the story 433
// A-1-7 move for internal/watchdog/domain/cooldown/ (folded from
// domain/cooldown/). Cooldown is a value object — should be a pure
// leaf with no horizontal-CA edges.
func TestWatchdogDomainCooldownNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "watchdog", "domain", "cooldown")
	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	checkWatchdogLeafImports(t, ctxRoot, "watchdog/domain/cooldown", bannedLayerRoots)
}

// TestWatchdogPersistenceNoBackwardsImports pins the story 434 A-1-8
// move for internal/watchdog/persistence/ (folded from
// infrastructure/database/repositories/cooldown_repository.go +
// no_better_counter_repository.go). The persistence layer is allowed to
// reach down into infrastructure/database for the GORM model types and
// up into application/ports for the repository contracts; everything
// else under the horizontal-CA roots must remain off-limits so a future
// repo cannot accidentally pull in a handler or use case.
func TestWatchdogPersistenceNoBackwardsImports(t *testing.T) {
	t.Parallel()

	ctxRoot := watchdogSubtree(t, "persistence")
	modPath := "github.com/alexmorbo/seasonfill"
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/infrastructure/database",
	}
	checkWatchdogSubtreeImports(t, ctxRoot, "watchdog/persistence", allowList)
}

// TestWatchdogInfrastructureNoBackwardsImports pins the story 434 A-1-8
// move for internal/watchdog/infrastructure/ (folded from
// infrastructure/watchdog and infrastructure/regrab). The state
// watchdog reads its per-instance Sonarr config via domain/instance and
// internal/config; the regrab cmd/server adapter satisfies the
// app/regrab QbitClientFactory port against infrastructure/qbit. No
// other horizontal-CA paths are allowed.
func TestWatchdogInfrastructureNoBackwardsImports(t *testing.T) {
	t.Parallel()

	ctxRoot := watchdogSubtree(t, "infrastructure")
	modPath := "github.com/alexmorbo/seasonfill"
	allowList := []string{
		modPath + "/domain/instance",
		modPath + "/infrastructure/qbit",
	}
	checkWatchdogSubtreeImports(t, ctxRoot, "watchdog/infrastructure", allowList)
}

// TestWatchdogRestNoBackwardsImports pins the story 434 A-1-8 move for
// internal/watchdog/rest/ (folded from interface/http/handlers/
// watchdog_*.go + rescan.go). The rest layer needs handlers for the
// shared WriteError/WriteInternalError/ParseLimit/HandleQueryErr
// helpers and the InstanceLister/InstanceRegistry types; dto/middleware
// for response shapes and routing wiring; infrastructure/database/
// repositories for the read-side row types the seasons handler maps;
// infrastructure/qbit for the on-demand torrents list shape; and
// application/{ports,rescan,scan} + domain/{release,series} for the
// rescan handler's collaborator surface.
func TestWatchdogRestNoBackwardsImports(t *testing.T) {
	t.Parallel()

	ctxRoot := watchdogSubtree(t, "rest")
	modPath := "github.com/alexmorbo/seasonfill"
	allowList := []string{
		modPath + "/application/ports",
		modPath + "/application/rescan",
		modPath + "/application/scan",
		modPath + "/domain/release",
		modPath + "/domain/series",
		modPath + "/infrastructure/database",
		modPath + "/infrastructure/qbit",
		modPath + "/interface/http/dto",
		modPath + "/interface/http/handlers",
		modPath + "/interface/http/middleware",
	}
	checkWatchdogSubtreeImports(t, ctxRoot, "watchdog/rest", allowList)
}

// watchdogSubtree resolves an internal/watchdog/<name> absolute path.
func watchdogSubtree(t *testing.T, name string) string {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return filepath.Join(repoRoot, "internal", "watchdog", name)
}

// checkWatchdogSubtreeImports walks ctxRoot and asserts that every .go
// file's imports under the four horizontal-CA roots
// (application/, domain/, infrastructure/, interface/) fall inside
// allowList. allowList entries are matched as exact paths or as
// prefixes (`pkg/` matches sub-packages too). Shared helper so each
// subtree's depcheck guard reuses the same boundary-message shape and
// the same allow-list semantics as the package-level umbrella in
// TestWatchdogNoBackwardsImports above.
func checkWatchdogSubtreeImports(t *testing.T, ctxRoot, label string, allowList []string) {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
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
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) && !isAllowed(v) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" ("+label+" must stay inside its allowList)")
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
		t.Errorf("%s has %d backward-import offenders — story 434 A-1-8 boundary breached:", label, len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}

// checkWatchdogLeafImports walks ctxRoot (a internal/watchdog/
// sub-tree) and asserts no .go file imports a horizontal-CA path
// outside an allowList. Centralized helper so leaf guards share the
// same boundary-message shape across stories.
func checkWatchdogLeafImports(t *testing.T, ctxRoot, label string, bannedLayerRoots []string) {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
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
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" ("+label+" is a leaf; use internal/shared/ or its own subtree)")
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
		t.Errorf("%s has %d backward-import offenders — story 433 A-1-7 boundary breached:", label, len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
