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

// TestGrabNoBackwardsImports enforces story 431 A-1-5 §3.3: every
// package under internal/grab/ MUST NOT import the legacy horizontal-
// CA dirs that the bounded context was migrated OUT of. Specifically
// the four old siblings that hosted grab code before the vertical-
// slice extraction:
//
//   - application/grab           (now internal/grab/app)
//   - domain/grab                (now internal/grab/domain)
//   - infrastructure/database/repositories/grab_repository*
//     (now internal/grab/persistence/*)
//   - interface/http/handlers/grab*  (now internal/grab/rest/*)
//
// And the general horizontal layers — internal/grab/ is a leaf
// context, so it must not reach into application/, domain/,
// infrastructure/, or interface/ at all (except via the
// internal/shared/ kernel and the cross-context ports.go contracts).
//
// Scope: production .go files and _test.go files alike. The story 431
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowlist) — mirrored from the admin guard so
// the two contexts share the same kernel-shape rules. See the admin
// godoc on tests/lint_admin_imports_test.go for the rationale on each
// entry; grab specifics noted inline below.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestGrabNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "grab")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedPrefixes := []string{
		modPath + "/application/grab",
		modPath + "/domain/grab",
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
	//
	// Grab-specific (vs admin) additions:
	//   * interface/http/handlers — the rest layer reuses ToGrabDTO,
	//     WriteError, WriteInternalError, InstanceRegistry from the
	//     catch-all handlers package. A later story will relocate these
	//     into per-context homes (likely interface/http/shared/).
	//   * application/scan — UseCase consumes scan.Instance via the
	//     handler InstanceRegistry shape; transitive include from the
	//     rest layer's reuse of handlers.InstanceRegistry.
	//   * application/torrentsync — persistence implements
	//     torrentsync.GrabHashLookup port (cross-context contract).
	//   * domain (root) + domain/cooldown + domain/decision + domain/release
	//     + domain/series — read-only cross-context types consumed by
	//     the rest layer (decision audit projection, cooldown lookup
	//     for the explicit-confirm path).
	//   * infrastructure/database — shared GORM model types
	//     (GrabRecordModel etc) the persistence repo references. Story
	//     449 (model split) will relocate them into per-context homes.
	//   * internal/config — webhook + scan test fixtures.
	//   * internal/runtime/crypto — qbit secret decryption hook in
	//     scan harness imported transitively via test helpers.
	//   * internal/shared/* — kernel imports are always allowed.
	allowList := []string{
		modPath + "/application/errtext",
		modPath + "/application/ports",
		modPath + "/application/scan",
		modPath + "/application/torrentsync",
		modPath + "/domain",
		modPath + "/domain/cooldown",
		modPath + "/domain/decision",
		modPath + "/domain/release",
		modPath + "/domain/series",
		modPath + "/infrastructure/database",
		modPath + "/interface/http/dto",
		modPath + "/interface/http/handlers",
		modPath + "/interface/http/middleware",
		modPath + "/internal/config",
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

			// Hard-banned exact-prefix matches (the old grab sibling
			// paths). Always a regression.
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
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (grab must use internal/shared/ or its own subtree)")
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
		t.Errorf("grab has %d backward-import offenders — vertical-slice boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}

// TestGrabPersistenceNoBackwardsImports is the focused sub-guard for
// story 431 A-1-5: the newly extracted internal/grab/persistence/ leaf
// MUST NOT reach back into the legacy
// infrastructure/database/repositories/ tree (catalog repos) — the
// shared txKey kernel was extracted into internal/shared/dbtx so this
// dependency is no longer needed. Any reference from grab persistence
// to a catalog repo (e.g. SonarrInstanceRepository, CooldownRepository,
// etc.) is a vertical-slice boundary breach.
//
// Allowed (carve-outs): infrastructure/database (shared GORM models;
// the story 449 model split will eventually relocate them too).
func TestGrabPersistenceNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "grab", "persistence")

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
				offenders = append(offenders, rel+": imports legacy catalog repo path "+v+" (grab persistence is a leaf; use internal/grab/ or internal/shared/ instead)")
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", ctxRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("grab/persistence has %d backward-import offenders — story 431 boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}

// TestGrabRestNoBackwardsImports is the focused sub-guard for story
// 431 A-1-5: the newly extracted internal/grab/rest/ leaf — Gin
// handler wiring for POST /decisions/{id}/grab + GET /instances/
// {name}/grabs/{id}/episode-files — MUST NOT reach back into the old
// catch-all interface/http/handlers/ tree EXCEPT through the explicit
// kernel-shaped carve-outs documented on TestGrabNoBackwardsImports.
//
// The general grab guard above would also catch a regression here at
// the layer-level "no interface/" rule, but this dedicated check pins
// the regression message to the exact story-431 rule so future
// boundary breaches self-document in test output without forcing
// operators to grep the PRD §3.1 grab slice.
//
// Scope: every .go file under internal/grab/rest (production +
// _test.go). Banned: any non-allowlisted import under interface/,
// application/, domain/, infrastructure/. The dedicated check
// re-uses the same allowList as TestGrabNoBackwardsImports so a
// single edit updates both — they share the carve-out set by design.
func TestGrabRestNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ctxRoot := filepath.Join(repoRoot, "internal", "grab", "rest")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	allowList := []string{
		modPath + "/application/errtext",
		modPath + "/application/ports",
		modPath + "/application/scan",
		modPath + "/application/torrentsync",
		modPath + "/domain",
		modPath + "/domain/cooldown",
		modPath + "/domain/decision",
		modPath + "/domain/release",
		modPath + "/domain/series",
		modPath + "/infrastructure/database",
		modPath + "/interface/http/dto",
		modPath + "/interface/http/handlers",
		modPath + "/interface/http/middleware",
		modPath + "/internal/config",
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
			for _, lr := range bannedLayerRoots {
				if strings.HasPrefix(v, lr) && !isAllowed(v) {
					rel, _ := filepath.Rel(repoRoot, path)
					offenders = append(offenders, rel+": imports horizontal-CA path "+v+" (grab/rest is a leaf; use internal/shared/ or its own subtree)")
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
		t.Errorf("grab/rest has %d backward-import offenders — story 431 boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
