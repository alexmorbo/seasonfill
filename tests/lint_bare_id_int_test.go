//go:build lint

package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestBareIDIntRegression scans the repo for struct fields and function
// parameters whose name matches a canonical typed-ID identifier
// (e.g. SeriesID, SonarrEpisodeID) but whose declared type is the bare
// underlying primitive (`int`, `int64`, `string`) instead of the typed
// alias from `internal/shared/domain/ids.go`.
//
// Background: PRD §6.3.1 Level 2 — primitive obsession defense. The
// A-5 story chain (391..407) migrated SeriesID, EpisodeID,
// SonarrSeriesID, SonarrEpisodeID, IMDBID, TMDBID, TVDBID, QbitHash and
// InstanceName to typed primitives. This test is the regression guard
// so a future PR cannot silently re-introduce a bare int field named
// SeriesID — the compiler would happily accept it (the typed alias is
// `type SeriesID int64`, so callers would re-mix types at the
// boundary, which is exactly what the migration was meant to prevent).
//
// Implementation note: forbidigo (which we use elsewhere) operates on
// identifiers and call expressions only — it cannot match type
// expressions in struct fields or function parameter lists. A
// dedicated AST scan is the only way to enforce the rule. Build tag
// `lint` keeps the cost out of the default test run; CI runs it via
// `make test-lint-rule`.
//
// Scope: the canonical-IDs allow-list (typedIDs) is intentionally
// narrow — it covers the IDs that already have typed aliases and that
// the codebase has migrated end-to-end. Adding a new typed ID requires
// a one-line addition here. Unmigrated IDs (PersonID, GenreID,
// KeywordID, ...) are documented as future-work in
// `internal/shared/domain/ids.go` and are NOT covered by this guard
// until their migration story lands.
func TestBareIDIntRegression(t *testing.T) {
	t.Parallel()

	// typedIDs maps the canonical typed-ID field/param name to the
	// underlying primitive(s) that would constitute a regression.
	// We list every primitive the ID could plausibly be re-typed
	// as so that, e.g., someone writing `SeriesID int` (forgot the
	// 64) is also caught.
	typedIDs := map[string]map[string]bool{
		"SeriesID":        {"int": true, "int64": true},
		"EpisodeID":       {"int": true, "int64": true},
		"SonarrSeriesID":  {"int": true, "int64": true},
		"SonarrEpisodeID": {"int": true, "int64": true},
		"TMDBID":          {"int": true, "int64": true},
		"TVDBID":          {"int": true, "int64": true},
		"IMDBID":          {"string": true},
		"QbitHash":        {"string": true},
		"InstanceName":    {"string": true},
	}

	// Directories to scan — domain and application layers only.
	//
	// The infrastructure/ and interface/http/dto/ layers legitimately
	// hold bare primitives at the wire boundary: external JSON
	// payloads (Sonarr, TMDB, OMDb), HTTP response DTOs, and error
	// types carry IDs as the over-the-wire primitive. Typed aliases
	// kick in once the value is mapped INTO the domain or application
	// layer — and that boundary is exactly what this guard protects.
	//
	// Adding new layers here requires that all wire DTOs in that
	// layer first migrate to the typed aliases. Until then, scoping
	// to domain+application catches the regression cases that matter
	// (the inner-core code paths) without the boundary noise.
	roots := []string{
		"application",
		"domain",
	}

	type hit struct {
		path  string
		line  int
		name  string
		typ   string
		where string
	}
	var hits []hit

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	fset := token.NewFileSet()
	skipFile := func(path string) bool {
		if strings.HasSuffix(path, "_test.go") {
			return true
		}
		if strings.HasSuffix(path, "_gen.go") || strings.HasSuffix(path, ".pb.go") {
			return true
		}
		// mocks/ dirs.
		if strings.Contains(path, "/mocks/") {
			return true
		}
		// The IDs declaration itself uses bare primitives — that's
		// the type definition, not a regression.
		if strings.HasSuffix(path, "internal/shared/domain/ids.go") {
			return true
		}
		return false
	}

	for _, root := range roots {
		abs := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if skipFile(path) {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				// Don't fail the test on a parse error in some
				// arbitrary file — log it; CI will still run the
				// real go build that would have caught it first.
				t.Logf("parse %s: %v", path, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.StructType:
					if x.Fields == nil {
						return true
					}
					for _, field := range x.Fields.List {
						typeName := bareTypeName(field.Type)
						if typeName == "" {
							continue
						}
						for _, name := range field.Names {
							if rules, ok := typedIDs[name.Name]; ok && rules[typeName] {
								pos := fset.Position(name.Pos())
								hits = append(hits, hit{
									path:  pos.Filename,
									line:  pos.Line,
									name:  name.Name,
									typ:   typeName,
									where: "struct field",
								})
							}
						}
					}
				case *ast.FuncType:
					if x.Params == nil {
						return true
					}
					for _, field := range x.Params.List {
						typeName := bareTypeName(field.Type)
						if typeName == "" {
							continue
						}
						for _, name := range field.Names {
							if rules, ok := typedIDs[name.Name]; ok && rules[typeName] {
								pos := fset.Position(name.Pos())
								hits = append(hits, hit{
									path:  pos.Filename,
									line:  pos.Line,
									name:  name.Name,
									typ:   typeName,
									where: "function param",
								})
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(hits) > 0 {
		var sb strings.Builder
		sb.WriteString("bare-id-int regression — typed-ID names declared as primitives:\n")
		for _, h := range hits {
			rel, _ := filepath.Rel(repoRoot, h.path)
			sb.WriteString("  ")
			sb.WriteString(rel)
			sb.WriteString(":")
			sb.WriteString(itoa(h.line))
			sb.WriteString("  ")
			sb.WriteString(h.where)
			sb.WriteString("  ")
			sb.WriteString(h.name)
			sb.WriteString(" ")
			sb.WriteString(h.typ)
			sb.WriteString("  (expected domain.")
			sb.WriteString(h.name)
			sb.WriteString(")\n")
		}
		t.Fatal(sb.String())
	}
}

// bareTypeName returns the identifier name of a *bare* type expression
// (`int`, `int64`, `string`), or "" for anything else (qualified
// selectors, pointers, slices, maps, channels — all forms that already
// route through a typed alias or are clearly not a primitive ID).
func bareTypeName(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	switch id.Name {
	case "int", "int64", "string":
		return id.Name
	}
	return ""
}

// itoa avoids strconv import (the build tag keeps this file out of
// the default build anyway, but minimal imports keep the regression
// guard fast to parse).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
