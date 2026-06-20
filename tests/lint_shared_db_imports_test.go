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

// TestSharedDBNoBackwardsImports enforces story 449 A-1-23 §3.2: the
// GORM kernel — Open/Migrate/Ping, canon GORM model structs, DSN
// redaction, and the embedded SQL migration corpus — lives in
// internal/shared/db/ as part of the shared kernel (PRD §3.2). The
// kernel is imported by every vertical context's persistence/, the
// runtime bootstrap, and the testhelpers Postgres/SQLite drivers; it
// sits ABOVE every vertical context and BELOW only the other kernel
// packages (internal/shared/*) plus internal/config + internal/logger
// (kernel-adjacent bootstrap helpers).
//
// Like every kernel package, shared/db MUST NOT import horizontal-CA
// layers (application/, domain/, infrastructure/, interface/) and MUST
// NOT import vertical-slice contexts (internal/<ctx>/) other than
// internal/shared/ — except via the explicit allowList carve-outs
// documented below.
//
// Scope: production .go files and _test.go files alike. The story 449
// move was structural; no test should suddenly reach into the legacy
// tree either.
//
// Carve-outs (explicit allowList) — kernel-adjacent bootstrap:
//
//   - internal/config — Open(cfg config.DatabaseConfig) reads the
//     driver/DSN/pool tunables off the typed config struct. The
//     config package is bootstrap-layer and intentionally has no
//     reverse dependencies into the kernel.
//   - internal/logger — NewGormLogger wraps the slog handler into a
//     GORM-compatible logger. logger is kernel-adjacent and shared
//     by every package that emits structured logs.
//
// No vertical-slice carve-outs: the GORM kernel is dialect-agnostic
// and table-agnostic from a runtime standpoint; the canon model structs
// are referenced BY contexts, not the other way around.
//
// Run via: `make test-lint-rule` (lint build tag).
func TestSharedDBNoBackwardsImports(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	ctxRoot := filepath.Join(repoRoot, "internal", "shared", "db")

	modPath := "github.com/alexmorbo/seasonfill"
	bannedLayerRoots := []string{
		modPath + "/application/",
		modPath + "/domain/",
		modPath + "/infrastructure/",
		modPath + "/interface/",
	}
	// Vertical-slice contexts (internal/<ctx>/) are banned by default;
	// the kernel must not depend on a single context. internal/shared/
	// is always allowed (kernel sibling).
	bannedVerticalSlicePrefixes := []string{
		modPath + "/internal/admin/",
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
				offenders = append(offenders, rel+": imports horizontal-CA or vertical-slice path "+v+" (shared/db kernel must stay inside its allowList)")
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", ctxRoot, walkErr)
	}

	if len(offenders) > 0 {
		t.Errorf("shared/db has %d backward-import offenders — story 449 A-1-23 kernel boundary breached:", len(offenders))
		for _, o := range offenders {
			t.Errorf("  %s", o)
		}
	}
}
