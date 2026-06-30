//go:build lint

package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
	// Story 453 (A-1-27) migrated the legacy horizontal `application/`
	// and `domain/` directories into per-context vertical slices. The
	// scan now walks `internal/<context>/app/` and `internal/<context>/
	// domain/` for every bounded context, catching the same inner-core
	// regression cases.
	//
	// The persistence/ + rest/ layers (and the shared/clients/* wire
	// adapters) legitimately hold bare primitives at the wire boundary:
	// external JSON payloads (Sonarr, TMDB, OMDb), HTTP response DTOs,
	// and error types carry IDs as the over-the-wire primitive. Typed
	// aliases kick in once the value is mapped INTO the domain or
	// application layer — and that boundary is exactly what this guard
	// protects.
	//
	// Adding a new context requires no change here — the loop below
	// auto-discovers every `internal/*/app/` and `internal/*/domain/`
	// directory at scan time.
	roots := []string{}

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
		// The values/ kit (Story 557 / E-1-A0) declarations also wrap
		// bare primitives in private fields — that IS the type
		// definition, not a regression.
		if strings.Contains(filepath.ToSlash(path), "internal/shared/domain/values/") {
			return true
		}
		return false
	}

	internalAbs := filepath.Join(repoRoot, "internal")
	contextEntries, err := os.ReadDir(internalAbs)
	if err != nil {
		t.Fatalf("read internal/: %v", err)
	}
	for _, ent := range contextEntries {
		if !ent.IsDir() {
			continue
		}
		for _, layer := range []string{"app", "domain"} {
			candidate := filepath.Join(internalAbs, ent.Name(), layer)
			if _, statErr := filepath.Glob(candidate); statErr == nil {
				roots = append(roots, filepath.Join("internal", ent.Name(), layer))
			}
		}
	}

	for _, root := range roots {
		abs := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Skip nonexistent layer dirs without failing the test.
				return nil
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
// (`int`, `int64`, `string`, `float64`), or "" for anything else
// (qualified selectors, pointers, slices, maps, channels — all forms
// that already route through a typed alias or are clearly not a
// primitive ID/value). `float64` was added in Story 557 (E-1-A0) for
// the Score VO guard.
func bareTypeName(expr ast.Expr) string {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	switch id.Name {
	case "int", "int64", "string", "float64":
		return id.Name
	}
	return ""
}

// TestBareValuePrimitivesRegression scans NEW DTO packages for struct
// fields and function params whose name matches a canonical
// value-object identifier (Year, Score, LanguageTag, MediaHash, …)
// but whose declared type is the bare underlying primitive instead
// of the VO from internal/shared/domain/values/.
//
// Story 557 (E-1-A0) shipped the VO kit. Phase 2 DTOs (B1/B2/B3)
// adopt the VOs end-to-end. This guard fires only on the Phase 2+
// package allow-list — legacy A-5-and-earlier code is intentionally
// excluded (its retrofit is the future E-2 epic).
//
// Adding a new typed-VO field requires a one-line addition to
// typedValues below.
func TestBareValuePrimitivesRegression(t *testing.T) {
	t.Parallel()

	typedValues := map[string]map[string]bool{
		// Year semantics — anything called Year/YearStart/YearEnd should be values.Year, not int.
		"Year":      {"int": true, "int64": true},
		"YearStart": {"int": true, "int64": true},
		"YearEnd":   {"int": true, "int64": true},
		// Runtime / duration.
		"RuntimeMinutes": {"int": true, "int64": true},
		"Minutes":        {"int": true, "int64": true},
		// Score / rating.
		"Score":     {"float64": true},
		"VoteCount": {"int": true, "int64": true},
		"Votes":     {"int": true, "int64": true},
		// Lang / locale.
		"LanguageTag":      {"string": true},
		"Lang":             {"string": true},
		"OriginalLanguage": {"string": true},
		"LangCode":         {"string": true},
		// Geo.
		"CountryCode": {"string": true},
		// Media.
		"PosterAsset":   {"string": true},
		"BackdropAsset": {"string": true},
		"LogoAsset":     {"string": true},
		"MediaHash":     {"string": true},
		// Trailer.
		"TrailerKey": {"string": true},
		// Content / status enums.
		"ContentRating": {"string": true},
		"SeriesStatus":  {"string": true},
	}

	// Allow-list of Phase 2+ DTO package paths. F-R2-6: scope NARROW so
	// the guard never trips on legacy A-5-and-earlier code. Adding a
	// new Phase 2+ DTO package requires a one-line addition here.
	//
	// Why allow-list (not skip-list): explicit "this is where new code
	// lives" is easier to audit than an ever-growing skip list. Phase 2
	// stories (B1/B2/B3) introduce new DTO packages — they extend this
	// allow-list as they land.
	allowList := []string{
		"internal/seriesdetail/app/dto",
		"internal/discovery/app/dto",
		"internal/seriesdetail/rest",
		"internal/discovery/rest",
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
		if strings.Contains(filepath.ToSlash(path), "/mocks/") {
			return true
		}
		return false
	}

	for _, root := range allowList {
		abs := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// Allow-list entry may not exist yet (Phase 2 packages
				// not landed) — skip silently.
				return nil
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
							if rules, ok := typedValues[name.Name]; ok && rules[typeName] {
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
							if rules, ok := typedValues[name.Name]; ok && rules[typeName] {
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
		sb.WriteString("bare-value-primitive regression — typed-VO names declared as primitives:\n")
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
			sb.WriteString("  (expected values.")
			sb.WriteString(h.name)
			sb.WriteString(")\n")
		}
		t.Fatal(sb.String())
	}
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
