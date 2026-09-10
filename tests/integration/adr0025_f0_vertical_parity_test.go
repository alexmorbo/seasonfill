//go:build integration

// ADR-0025 F0 — conformance between the vertical-parity DECLARATION
// (internal/shared/verticals) and the CODE.
//
// Every check is written as a detector — "does the code carry this
// invariant TODAY?" — and compared with require.Equal against the
// declaration. That is what makes the suite red in BOTH directions: a
// Held the code lost fails, and a Gap the code silently closed fails just
// as loudly. There is deliberately no `if declared == Gap { skip }`
// anywhere in this file; such a test would only ever catch regressions
// and would let the declaration rot the moment a phase closed a hole.
//
// Where an invariant cannot be honestly probed at F0 the cell is listed
// in f0Undetectable with a reason instead of getting a fake probe. Every
// registry row must be resolved by EXACTLY ONE of: a detector, a
// deferred-premise guard, or an explicit undetectable entry — silence is
// impossible.
package integration

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/shared/verticals"
)

// ---------------------------------------------------------------------
// repo-relative source access
// ---------------------------------------------------------------------

// f0RepoRoot resolves the module root from this file's own location, so
// the suite never hardcodes a machine path.
func f0RepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) failed")
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	require.NoError(t, err)
	return root
}

func f0Read(t *testing.T, root string, rel ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{root}, rel...)...)
	b, err := os.ReadFile(p)
	require.NoErrorf(t, err, "read %s", p)
	return string(b)
}

// f0StringLiterals returns every Go string literal in one file. Using the
// parser rather than raw text keeps doc comments out of the scan — several
// of these files MENTION the very SQL fragments and metric names being
// probed, so a naive strings.Contains would report an invariant that is
// only documented, not implemented.
func f0StringLiterals(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s", path)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			if v, uerr := strconv.Unquote(lit.Value); uerr == nil {
				out = append(out, v)
			} else {
				out = append(out, lit.Value)
			}
		}
		return true
	})
	return out
}

// f0CodeIdentifiers returns every identifier that appears in the AST of
// one file. The parser deliberately runs WITHOUT parser.ParseComments, so
// comments are not part of the tree at all — a `// TODO: EnrichmentError
// in F1` cannot flip a detector, only a real reference in code can. Same
// trap f0StringLiterals guards against, and not a theoretical one here:
// F1 and F2 edit exactly the files these detectors read.
func f0CodeIdentifiers(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s", path)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out = append(out, id.Name)
		}
		return true
	})
	require.NotEmptyf(t, out, "no identifiers parsed out of %s — the detector would "+
		"silently report the invariant as absent", path)
	return out
}

// f0CodeMentions reports whether any IDENTIFIER in the file contains sub.
// Substring rather than equality on purpose: "EnrichmentError" must match
// the type, the field (EnrichmentErrors) and the method
// (recordEnrichmentError) alike.
func f0CodeMentions(t *testing.T, path, sub string) bool {
	t.Helper()
	for _, id := range f0CodeIdentifiers(t, path) {
		if strings.Contains(id, sub) {
			return true
		}
	}
	return false
}

// f0LiteralMentions reports whether any STRING LITERAL in the file
// contains sub. Used for event/metric name markers, which live in
// literals — and, in the files this suite probes, also in prose comments
// that must NOT count.
func f0LiteralMentions(t *testing.T, path, sub string) bool {
	t.Helper()
	for _, lit := range f0StringLiterals(t, path) {
		if strings.Contains(lit, sub) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------
// detectors — "does the code carry this invariant TODAY?"
// ---------------------------------------------------------------------

// f0DetectFailureJournal reports whether the named worker files journal
// enrichment failures at all.
func f0DetectFailureJournal(t *testing.T, root string, rels ...[]string) bool {
	t.Helper()
	for _, rel := range rels {
		path := filepath.Join(append([]string{root}, rel...)...)
		if f0CodeMentions(t, path, "EnrichmentError") {
			return true
		}
	}
	return false
}

// f0DetectPickerBreaker reports whether EVERY tier arm of a tiered picker
// carries the terminal-failure gate.
//
// arms  = number of UNION ALL'd SELECTs in the picker's SQL literal
// gates = number of `ee.attempts >` predicates in that same literal
//
// Written as `gates >= arms` rather than a hardcoded count so that adding
// a sixth tier without its gate turns the test red instead of passing on
// a stale constant.
func f0DetectPickerBreaker(t *testing.T, root string, rel ...string) bool {
	t.Helper()
	path := filepath.Join(append([]string{root}, rel...)...)
	var b strings.Builder
	for _, lit := range f0StringLiterals(t, path) {
		if strings.Contains(lit, "UNION ALL") {
			b.WriteString(lit)
			b.WriteString("\n")
		}
	}
	sql := b.String()
	require.NotEmptyf(t, sql, "no UNION ALL SQL literal found in %s — the detector would "+
		"silently pass; the picker was probably restructured", path)
	arms := strings.Count(sql, "UNION ALL") + 1
	gates := strings.Count(sql, "ee.attempts >")
	return gates >= arms
}

// f0RetrySweepSources returns the Source constant names handed to
// ListDueForRetry by the nightly wiring.
func f0RetrySweepSources(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "wiring", "enrichment.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s", path)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fun, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || fun.Sel.Name != "ListDueForRetry" {
			return true
		}
		for _, arg := range call.Args {
			if sel, ok := arg.(*ast.SelectorExpr); ok &&
				strings.HasPrefix(sel.Sel.Name, "Source") {
				out = append(out, sel.Sel.Name)
			}
		}
		return true
	})
	require.NotEmptyf(t, out, "no ListDueForRetry arms found in %s — the detector would "+
		"silently report every vertical as a gap", path)
	return out
}

// f0GrabRecordFields returns the field names of grab.Record.
func f0GrabRecordFields(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "grab", "domain", "grab.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s", path)
	var fields []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Record" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fld := range st.Fields.List {
			for _, nm := range fld.Names {
				fields = append(fields, nm.Name)
			}
		}
		return false
	})
	require.NotEmptyf(t, fields, "grab.Record not found in %s", path)
	return fields
}

// f0ObservabilityHasPrefix reports whether any production string literal
// under internal/observability/ carries the given metric-name prefix.
func f0ObservabilityHasPrefix(t *testing.T, root, prefix string) bool {
	t.Helper()
	dir := filepath.Join(root, "internal", "observability")
	found := false
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		for _, lit := range f0StringLiterals(t, path) {
			if strings.Contains(lit, prefix) {
				found = true
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.NotZerof(t, scanned, "no production .go files under %s", dir)
	return found
}

// f0SourceConstants returns value→constName for every `X Source = "..."`
// declaration in the enrichment domain package.
func f0SourceConstants(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, "internal", "enrichment", "domain", "enrichment")
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoErrorf(t, perr, "parse %s", name)
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				ident, ok := vs.Type.(*ast.Ident)
				if !ok || ident.Name != "Source" {
					continue
				}
				for i, nm := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, uerr := strconv.Unquote(lit.Value)
					require.NoError(t, uerr)
					out[v] = nm.Name
				}
			}
		}
	}
	require.NotEmptyf(t, out, "no Source constants found under %s", dir)
	return out
}

// ---------------------------------------------------------------------
// detector wiring
// ---------------------------------------------------------------------

type f0Probe func(t *testing.T, root string) bool

// f0Detectors maps a Held/Gap cell to its "does the code carry it today?"
// probe.
var f0Detectors = map[verticals.Key]f0Probe{
	{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalSeries}: func(t *testing.T, root string) bool {
		path := filepath.Join(root, "internal", "enrichment", "app", "series_worker.go")
		return f0CodeMentions(t, path, "recordEnrichmentError") &&
			f0CodeMentions(t, path, "EnrichmentErrors")
	},
	{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		return f0DetectFailureJournal(t, root,
			[]string{"internal", "enrichment", "app", "movie_worker.go"},
			[]string{"internal", "enrichment", "app", "movie_ports.go"},
		)
	},
	{Invariant: verticals.InvariantPickerBreaker, Vertical: verticals.VerticalSeries}: func(t *testing.T, root string) bool {
		return f0DetectPickerBreaker(t, root,
			"internal", "enrichment", "persistence", "series_refresh_query.go")
	},
	{Invariant: verticals.InvariantPickerBreaker, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		return f0DetectPickerBreaker(t, root,
			"internal", "enrichment", "persistence", "movie_refresh_query.go")
	},
	{Invariant: verticals.InvariantRetrySweep, Vertical: verticals.VerticalSeries}: func(t *testing.T, root string) bool {
		for _, s := range f0RetrySweepSources(t, root) {
			if s == "SourceTMDBSeries" {
				return true
			}
		}
		return false
	},
	{Invariant: verticals.InvariantRetrySweep, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		for _, s := range f0RetrySweepSources(t, root) {
			if strings.Contains(s, "Movie") {
				return true
			}
		}
		return false
	},
	{Invariant: verticals.InvariantLoopDeclaresTypes, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		// ADR-0025 F2 names the marker verbatim: "one INFO
		// regrab_skipped_unsupported_type per swap instead of a WARN every
		// 30 minutes". When F2 lands the explicit supported-type
		// declaration this probe flips to true, and the still-declared Gap
		// turns the suite red — which is precisely how the registry gets
		// flipped in the same story rather than months later.
		path := filepath.Join(root, "cmd", "server", "loops", "regrab.go")
		return f0LiteralMentions(t, path, "regrab_skipped_unsupported_type")
	},
	{Invariant: verticals.InvariantRegrabSupported, Vertical: verticals.VerticalSeries}: func(t *testing.T, root string) bool {
		fields := f0GrabRecordFields(t, root)
		var hasSeries, hasSeason bool
		for _, f := range fields {
			switch f {
			case "SeriesID":
				hasSeries = true
			case "SeasonNumber":
				hasSeason = true
			}
		}
		return hasSeries && hasSeason
	},
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalSeries}: func(t *testing.T, root string) bool {
		return f0ObservabilityHasPrefix(t, root, "seasonfill_enrichment_")
	},
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		// Subject = search bounded context (see the Status.Subject note on
		// this cell): the movie vertical already exports metrics, the
		// search bc does not.
		return f0ObservabilityHasPrefix(t, root, "seasonfill_search_")
	},
	{Invariant: verticals.InvariantDomainLogger, Vertical: verticals.VerticalSeries}: func(t *testing.T, _ string) bool {
		_, ok := sharedports.AllowedDomains["enrichment"]
		return ok
	},
	{Invariant: verticals.InvariantDomainLogger, Vertical: verticals.VerticalMovie}: func(t *testing.T, _ string) bool {
		// movie_worker.go:105 wires DomainLogger(slog.Default(), "enrichment"),
		// the same closed-list value series_worker.go:208 uses.
		_, ok := sharedports.AllowedDomains["enrichment"]
		return ok
	},
}

// f0DeferredGuards keep a Deferred cell from rotting: the guard asserts
// that the PREMISE of the deferral still holds. A true result means "the
// reason to defer is still true".
var f0DeferredGuards = map[verticals.Key]f0Probe{
	{Invariant: verticals.InvariantRegrabSupported, Vertical: verticals.VerticalMovie}: func(t *testing.T, root string) bool {
		// Premise: there is no movie-grab infrastructure at all —
		// grab.Record carries no movie field (grab.go:58,60 key by
		// SeriesID + SeasonNumber). If that ever changes, the deferral
		// must be revisited rather than silently inherited.
		for _, f := range f0GrabRecordFields(t, root) {
			if strings.Contains(f, "Movie") {
				return false
			}
		}
		return true
	},
}

// f0Undetectable lists cells that cannot be honestly probed at F0. An
// entry here is a declaration in its own right: the reason is mandatory
// and the suite fails if a cell is neither probed nor listed.
var f0Undetectable = map[verticals.Key]string{
	{Invariant: verticals.InvariantLoopDeclaresTypes, Vertical: verticals.VerticalSeries}: "" +
		"The ADR grounds this cell in live production observation " +
		"(torrentsync_reconciler_start/_done with instance=radarr every ~30s, no errors), " +
		"not in a property of the source tree. torrentsync declares nothing about instance " +
		"types today — it simply happens to be genuinely type-neutral — so any static scan " +
		"would be an imitation of a check. F2 introduces the explicit supported-type " +
		"declaration that makes BOTH sides of this invariant detectable; the probe belongs " +
		"in that story, not here.",
}

// ---------------------------------------------------------------------
// TESTS
// ---------------------------------------------------------------------

// TestADR0025_F0_RegistryMatchesCode is the two-way conformance check.
func TestADR0025_F0_RegistryMatchesCode(t *testing.T) {
	t.Parallel()
	root := f0RepoRoot(t)

	require.Empty(t, verticals.Validate(), "the declaration itself is malformed")

	for _, entry := range verticals.All() {
		t.Run(entry.Key.String(), func(t *testing.T) {
			probe, hasProbe := f0Detectors[entry.Key]
			guard, hasGuard := f0DeferredGuards[entry.Key]
			reason, undetectable := f0Undetectable[entry.Key]

			if entry.Status.State == verticals.StateDeferred {
				require.False(t, hasProbe,
					"a deferred cell must not carry a Held/Gap probe — it asserts nothing "+
						"about whether the code carries the invariant")
				require.NotEmpty(t, entry.Status.Reason,
					"deferred without a reason (ADR-0025 R2-bis)")
				require.Truef(t, hasGuard,
					"deferred cell %s has no premise guard — the deferral could rot in "+
						"silence", entry.Key)
				require.Truef(t, guard(t, root),
					"the premise of deferring %s no longer holds: %s",
					entry.Key, entry.Status.Reason)
				return
			}

			require.Falsef(t, hasGuard,
				"%s is not deferred but carries a deferred-premise guard", entry.Key)
			require.Truef(t, hasProbe != undetectable,
				"%s must be resolved by EXACTLY ONE of a detector or an explicit "+
					"undetectable entry (detector=%v, undetectable=%v)",
				entry.Key, hasProbe, undetectable)

			if undetectable {
				require.NotEmptyf(t, reason,
					"%s is declared undetectable without a reason", entry.Key)
				t.Logf("%s: not probed at F0 — %s", entry.Key, reason)
				return
			}

			wantHeld := entry.Status.State == verticals.StateHeld
			gotHeld := probe(t, root)
			require.Equalf(t, wantHeld, gotHeld,
				"declaration/code mismatch for %s: declared %q, code carries it = %v.\n"+
					"Evidence on file: %s\n"+
					"If the code changed, flip the cell in internal/shared/verticals; if the "+
					"declaration is right, the code regressed.",
				entry.Key, entry.Status.State, gotHeld, entry.Status.Evidence)
		})
	}
}

// TestADR0025_F0_UndetectableSetIsLocked is the symmetric twin of the
// deferred-count lock in internal/shared/verticals/verticals_test.go
// ("exactly one deferred cell today"). Without it the two escape hatches
// would be guarded asymmetrically: the number of Deferred cells is
// pinned, f0Undetectable was not — so a detector that went red could be
// quietly silenced by moving its cell in here with a well-worded reason,
// and nothing would notice. That is exactly the disease ADR-0025 R2-bis
// treats, turned on the test suite itself. The set MAY grow, but only
// deliberately: bump the count here and state in the entry why an honest
// probe is impossible.
func TestADR0025_F0_UndetectableSetIsLocked(t *testing.T) {
	t.Parallel()

	require.Lenf(t, f0Undetectable, 1,
		"exactly one undetectable cell today: loop_declares_types/series. Growing this "+
			"set silences a conformance probe — if that is genuinely intended, say why "+
			"in the entry and bump this count in the same change (ADR-0025 R2-bis)")

	loopSeries := verticals.Key{
		Invariant: verticals.InvariantLoopDeclaresTypes,
		Vertical:  verticals.VerticalSeries,
	}
	require.Containsf(t, f0Undetectable, loopSeries,
		"%s is the one cell the ADR grounds in live production observation rather than "+
			"in a property of the source tree", loopSeries)

	for k, reason := range f0Undetectable {
		require.NotEmptyf(t, reason,
			"%s: declared undetectable without a reason — an unprobed cell must say why "+
				"an honest probe is impossible (ADR-0025 R2-bis)", k)
	}
}

// TestADR0025_F0_JournalledSourcesValid is ADR-0025 R4-bis: every
// JOURNALLED Source must pass IsValid(); the live-reachability flags are
// declared known-exceptions and must NOT pass.
func TestADR0025_F0_JournalledSourcesValid(t *testing.T) {
	t.Parallel()
	root := f0RepoRoot(t)

	consts := f0SourceConstants(t, root)
	for value, name := range consts {
		valid := enrichdomain.Source(value).IsValid()
		if verticals.IsJournalledSourceException(value) {
			require.Falsef(t, valid,
				"%s (%q) is a declared known-exception but now passes IsValid(). Either the "+
					"exception was fixed (remove it from JournalledSourceExceptions) or the "+
					"enum drifted.", name, value)
			continue
		}
		require.Truef(t, valid,
			"%s (%q) is a journalled source but does NOT pass IsValid() — persistence would "+
				"reject it (ADR-0025 R4-bis)", name, value)
	}

	// Every declared exception must name a real constant, and its quote
	// must still be literally present in the file that establishes it.
	degraded := f0Read(t, root, "internal", "enrichment", "domain", "enrichment", "degraded.go")
	for _, e := range verticals.JournalledSourceExceptions {
		_, ok := consts[e.Source]
		require.Truef(t, ok,
			"known-exception %q does not correspond to any declared Source constant", e.Source)
		require.Containsf(t, degraded, e.Quote,
			"the citation for known-exception %q is no longer present in degraded.go", e.Source)
	}
}

// TestADR0025_F0_VerticalsPackageHasNoInternalImports duplicates
// tests/lint_shared_verticals_imports_test.go on purpose: the CI `lint`
// job runs golangci-lint only and never executes the lint-tagged suite,
// so without this copy the ADR-0025 R3 boundary would be unguarded on
// every PR. This package DOES run in CI (./tests/... in the sqlite lane,
// ./tests/integration/... in the postgres lane).
func TestADR0025_F0_VerticalsPackageHasNoInternalImports(t *testing.T) {
	t.Parallel()
	root := f0RepoRoot(t)
	dir := filepath.Join(root, "internal", "shared", "verticals")
	const modPath = "github.com/alexmorbo/seasonfill"

	fset := token.NewFileSet()
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		require.NoErrorf(t, perr, "parse %s", path)
		scanned++
		for _, imp := range f.Imports {
			v := strings.Trim(imp.Path.Value, `"`)
			require.Falsef(t, v == modPath || strings.HasPrefix(v, modPath+"/"),
				"%s imports %s — the parity registry must import stdlib ONLY (ADR-0025 R3)",
				path, v)
		}
		return nil
	})
	require.NoError(t, err)
	require.NotZerof(t, scanned, "no .go files under %s — the scan covered nothing", dir)
}

// TestADR0025_F0_PickerBreakerBehaviour is the behavioural half of the
// picker_breaker check: it seeds a terminal enrichment_errors row and
// drives the REAL pickers, not a copy of their SQL. A copied query would
// be a tautology — it would keep passing after the production statement
// changed.
//
// Postgres-only (the harness mirrors f1_4_movie_reenrich_backfill_test.go):
// the sqlite lane still runs every static check in this file. When the
// postgres lane is off this test SKIPS loudly instead of reporting a bare
// PASS with no subtests — a silent green would read as "verified" while
// nothing behavioural was exercised at all.
func TestADR0025_F0_PickerBreakerBehaviour(t *testing.T) {
	backends := allD1Backends(t)
	pg := -1
	for i, b := range backends {
		if b.name == "postgres" {
			pg = i
			break
		}
	}
	if pg < 0 {
		t.Skip("postgres backend is not enabled, so the BEHAVIOURAL half of picker_breaker " +
			"did NOT run: nothing seeded a terminal enrichment_errors row and nothing drove " +
			"the real series/movie pickers. A green line here means \"not checked\", not " +
			"\"checked and fine\" — the static detectors in this file did run. Enable with " +
			"SEASONFILL_TEST_POSTGRES_ENABLE=1 (or SEASONFILL_TEST_POSTGRES_DSN=...); the CI " +
			"postgres lane runs it on every push.")
	}

	b := backends[pg]
	t.Run(b.name, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		db, m, cleanup := b.migrate(t)
		t.Cleanup(cleanup)
		require.NoError(t, m.Up())

		gdb, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: db}), &gorm.Config{})
		require.NoError(t, err)

		seriesRepo := enrichpersistence.NewSeriesRepository(gdb)
		movieRepo := enrichpersistence.NewMovieRepository(gdb)
		errRepo := enrichpersistence.NewEnrichmentErrorsRepository(gdb)

		now := time.Now().UTC().Truncate(time.Microsecond)
		ttl := enrichdomain.DefaultRefreshTTL()

		// --- series -------------------------------------------------
		seriesTMDB := shareddomain.TMDBID(1399)
		seriesID, err := seriesRepo.Upsert(ctx, series.Canon{
			TMDBID:    &seriesTMDB,
			Hydration: series.HydrationFull,
		})
		require.NoError(t, err)
		require.NotZero(t, seriesID)

		require.Truef(t, f0SeriesPicked(t, ctx, seriesRepo, now, ttl, seriesID),
			"baseline: an un-journalled series must be picked, otherwise the probe below "+
				"proves nothing")

		require.NoError(t, errRepo.RecordFailure(ctx, enrichdomain.EnrichmentError{
			EntityType:  enrichdomain.EntityTypeSeries,
			EntityID:    int64(seriesID),
			Source:      enrichdomain.SourceTMDBSeries,
			LastError:   "ADR-0025 F0 parity probe",
			Attempts:    99,
			FirstSeenAt: now,
			LastSeenAt:  now,
		}))
		seriesBreakerHeld := !f0SeriesPicked(t, ctx, seriesRepo, now, ttl, seriesID)

		// --- movie --------------------------------------------------
		movieTMDB := shareddomain.TMDBID(603)
		movieID, err := movieRepo.Upsert(ctx, movie.Canon{
			TMDBID:    &movieTMDB,
			Title:     "ADR-0025 F0 parity probe",
			Hydration: movie.HydrationFull,
		})
		require.NoError(t, err)
		require.NotZero(t, movieID)

		require.Truef(t, f0MoviePicked(t, ctx, movieRepo, now, ttl, movieID),
			"baseline: an un-journalled movie must be picked, otherwise the probe below "+
				"proves nothing")

		// Seeded with a raw INSERT on purpose: EntityTypeMovie /
		// SourceTMDBMovie do not exist yet (that IS the
		// failure_journal/movie gap), so RecordFailure would reject the
		// row at its own validator. The raw path is legitimate —
		// enrichment_errors.entity_type / .source are plain
		// `text NOT NULL` with no CHECK, enum or FK in either dialect
		// (infrastructure/database/schema/schema.go:2195-2198:
		// "enforced at the use-case layer … NOT by DB constraint").
		require.NoError(t, gdb.WithContext(ctx).Exec(
			`INSERT INTO enrichment_errors
			   (entity_type, entity_id, source, last_error, attempts, first_seen_at, last_seen_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			"movie", int64(movieID), "tmdb_movie",
			"ADR-0025 F0 parity probe", 99, now, now).Error)
		movieBreakerHeld := !f0MoviePicked(t, ctx, movieRepo, now, ttl, movieID)

		// --- conformance --------------------------------------------
		seriesDecl := verticals.MustLookup(
			verticals.InvariantPickerBreaker, verticals.VerticalSeries)
		movieDecl := verticals.MustLookup(
			verticals.InvariantPickerBreaker, verticals.VerticalMovie)

		require.Equalf(t, seriesDecl.State == verticals.StateHeld, seriesBreakerHeld,
			"picker_breaker/series declared %q but the live series picker %s a "+
				"terminally-journalled series",
			seriesDecl.State, f0PickVerb(!seriesBreakerHeld))
		require.Equalf(t, movieDecl.State == verticals.StateHeld, movieBreakerHeld,
			"picker_breaker/movie declared %q but the live movie picker %s a "+
				"terminally-journalled movie — if F1 landed the gate, flip the cell in "+
				"internal/shared/verticals",
			movieDecl.State, f0PickVerb(!movieBreakerHeld))
	})
}

func f0PickVerb(picked bool) string {
	if picked {
		return "STILL PICKS"
	}
	return "EXCLUDES"
}

func f0SeriesPicked(
	t *testing.T,
	ctx context.Context,
	repo *enrichpersistence.SeriesRepository,
	now time.Time,
	ttl enrichdomain.RefreshTTL,
	want shareddomain.SeriesID,
) bool {
	t.Helper()
	cands, err := repo.PickRefreshCandidates(ctx, now, ttl, 500)
	require.NoError(t, err)
	for _, c := range cands {
		if c.SeriesID == want {
			return true
		}
	}
	return false
}

func f0MoviePicked(
	t *testing.T,
	ctx context.Context,
	repo *enrichpersistence.MovieRepository,
	now time.Time,
	ttl enrichdomain.RefreshTTL,
	want shareddomain.MovieID,
) bool {
	t.Helper()
	cands, err := repo.PickMovieRefreshCandidates(ctx, now, ttl, 500)
	require.NoError(t, err)
	for _, c := range cands {
		if c.MovieID == want {
			return true
		}
	}
	return false
}
