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

// TestNoGormInApplication enforces PRD §6 A-3: the application layer must
// not depend on gorm.io/gorm. Story 421 (A-3 mini) removed the last
// production import; this guard prevents a regression.
//
// Story 453 (A-1-27) migrated the horizontal `application/` directory into
// per-context vertical slices (`internal/*/app/*`), so the scan now walks
// every per-context `app/` package. The rule itself is unchanged.
//
// Scope: production files only (non-_test.go). The PRD §6 carve-out
// explicitly allows tests to hold concrete types — a future story may
// migrate those too, but the layer-rule for production is the hard
// boundary.
//
// Implementation note: forbidigo (used elsewhere) operates on identifiers
// and call expressions only — it cannot match import paths. A dedicated
// AST scan is the only way to enforce the rule. Build tag `lint` keeps
// the cost out of the default test run; CI runs it via
// `make test-lint-rule`.
func TestNoGormInApplication(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	internalRoot := filepath.Join(repoRoot, "internal")

	fset := token.NewFileSet()
	var offenders []string

	walkErr := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Only application-layer files: internal/<ctx>/app/...
		rel, _ := filepath.Rel(internalRoot, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) < 3 || parts[1] != "app" {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Logf("parse %s: %v", path, perr)
			return nil
		}
		for _, imp := range f.Imports {
			v := strings.Trim(imp.Path.Value, `"`)
			if v == "gorm.io/gorm" || strings.HasPrefix(v, "gorm.io/gorm/") {
				relRepo, _ := filepath.Rel(repoRoot, path)
				offenders = append(offenders, relRepo)
				break
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk internal/*/app/: %v", walkErr)
	}

	if len(offenders) > 0 {
		t.Fatalf("internal/*/app/ production files MUST NOT import gorm.io/gorm (PRD §6 A-3). Offenders:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
