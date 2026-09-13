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
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// The repo-relative source paths the detectors read, spelled ONCE. Every probe
// and every f0Cell.Reads declaration uses these constants, so a cell cannot
// claim to read one file while its closure opens another — which is exactly the
// drift the B-46 binding rule (f0BindingViolations) exists to make impossible.
// Slash-separated; f0Path converts for the host OS.
const (
	f0RelSeriesWorker     = "internal/enrichment/app/series_worker.go"
	f0RelMovieWorker      = "internal/enrichment/app/movie_worker.go"
	f0RelSeriesPicker     = "internal/enrichment/persistence/series_refresh_query.go"
	f0RelMoviePicker      = "internal/enrichment/persistence/movie_refresh_query.go"
	f0RelWiringEnrichment = "internal/wiring/enrichment.go"
	f0RelGrabRecord       = "internal/grab/domain/grab.go"
	f0RelRegrabLoop       = "cmd/server/loops/regrab.go"
	f0RelSearchMetrics    = "internal/observability/search_metrics.go"
	f0RelEnrichMetrics    = "internal/observability/enrichment_refresh_metrics.go"
)

// f0OpenLog records, per running *testing.T, the repo-relative paths that were
// turned into absolute paths while a probe was running.
//
// Why a global keyed by t rather than a parameter: f0Path is the ONE funnel
// every detector goes through to reach a file, and it is reached through half a
// dozen helpers. Threading a recorder through all of them would leave the
// recorder optional, and an optional recorder is exactly the kind of soft spot
// this file exists to remove. Keyed by t because the top-level tests run in
// parallel; the mutex covers the map, and a probe only ever runs under one t.
var f0OpenLog = struct {
	mu sync.Mutex
	m  map[*testing.T]map[string]bool
}{m: map[*testing.T]map[string]bool{}}

// f0TrackOpens starts recording for t and returns a function that stops
// recording and hands back the sorted set of rel paths seen.
func f0TrackOpens(t *testing.T) func() []string {
	t.Helper()
	f0OpenLog.mu.Lock()
	f0OpenLog.m[t] = map[string]bool{}
	f0OpenLog.mu.Unlock()
	return func() []string {
		f0OpenLog.mu.Lock()
		defer f0OpenLog.mu.Unlock()
		set := f0OpenLog.m[t]
		delete(f0OpenLog.m, t)
		out := make([]string, 0, len(set))
		for rel := range set {
			out = append(out, rel)
		}
		sort.Strings(out)
		return out
	}
}

// f0Path turns a repo-relative slash path into an absolute host path, and
// RECORDS the rel it was handed while a probe is being tracked.
//
// This is the mechanism that makes the f0Cell docblock's central claim true
// (B-46 review #3, attack D). Before it, a cell could declare
// Reads: []string{f0RelMovieWorker, f0RelSeriesWorker} — an honest-looking
// two-file declaration — and open only c.Reads[1]. Every existing rule was
// satisfied: the binding rule saw the movie token in a read path, the probe
// contract saw c.Reads consumed, and the mis-binding lived in a ONE-CHARACTER
// index inside the closure. The registry reported Held with movie_worker.go
// gutted. Recording what is actually resolved and requiring it to EQUAL the
// declaration closes the LAZY form of that: a path declared but never resolved
// is now as loud as a path resolved but never declared. It does not close the
// eager form — a body that calls f0Paths on the whole declaration and then reads
// the wrong index resolves exactly what it declared. That remainder is named in
// the f0Cell docblock and pinned as a boundary case in
// TestADR0025_F0_ProbeOpenSetBites.
func f0Path(t *testing.T, root, rel string) string {
	t.Helper()
	f0OpenLog.mu.Lock()
	if set, tracking := f0OpenLog.m[t]; tracking {
		set[rel] = true
	}
	f0OpenLog.mu.Unlock()
	return filepath.Join(root, filepath.FromSlash(rel))
}

// f0Paths resolves EVERY rel in one go, before any short-circuit can skip one.
//
// Multi-file probes must call this first. A probe that resolved its paths lazily
// and returned false on the first missing thing would register a partial open
// set and trip the equality rule with a FALSE RED; resolving up front means the
// declaration is "opened" the moment the probe commits to it, and the equality
// check needs no exception for short-circuiting.
//
// What the prescription costs, because it is not free and review #4 found this
// docblock silent about it: for a probe that follows it, opened ≡ declared holds
// identically, so the equality rule can no longer tell c.Reads[0] from
// c.Reads[1] in that body. Short-circuit freedom is bought with index-swap
// detection. See the remainder list in the f0Cell docblock and the named
// boundary case in TestADR0025_F0_ProbeOpenSetBites.
func f0Paths(t *testing.T, root string, rels []string) []string {
	t.Helper()
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		out = append(out, f0Path(t, root, rel))
	}
	return out
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

// f0IdentUsed reports whether the EXACT identifier name appears anywhere in
// the AST of one file.
//
// B-46 replaced the former f0CodeMentions, which matched a SUBSTRING of any
// identifier. Substring matching was chosen so that one marker
// ("EnrichmentError") would match the type, the field (EnrichmentErrors) and
// the helper (recordEnrichmentError) at once — convenient, and a false-Held
// generator: "Source" matched SourceTMDBMovieLegacy, "Movie" matched
// MovieUnsupported, and any near-miss rename kept the cell green. Markers are
// now spelled out one per kind, and each kind gets the primitive that fits it:
// f0IdentUsed for "this must exist as a dependency / field / type", f0HasCalls
// for "this must actually be CALLED".
//
// Comments are not in the tree (parser runs WITHOUT parser.ParseComments), so a
// `// TODO: EnrichmentError in F1` still cannot flip anything — that is the F1
// guarantee, kept.
func f0IdentUsed(t *testing.T, path, name string) bool {
	t.Helper()
	for _, id := range f0CodeIdentifiers(t, path) {
		if id == name {
			return true
		}
	}
	return false
}

// f0LiteralExact reports whether want is a STRING LITERAL of the file, compared
// by EQUALITY.
//
// B-46: the former f0LiteralMentions used strings.Contains, so
// "regrab_skipped_unsupported_types" (plural — a plausible rename) satisfied a
// probe for "regrab_skipped_unsupported_type", and
// "seasonfill_search_requests_total_v2" satisfied one for
// "seasonfill_search_requests_total". An event or metric name IS its exact
// spelling: a dashboard querying the old name sees nothing after such a rename,
// so a detector that shrugs at it is measuring the wrong thing.
func f0LiteralExact(t *testing.T, path, want string) bool {
	t.Helper()
	for _, lit := range f0StringLiterals(t, path) {
		if lit == want {
			return true
		}
	}
	return false
}

// f0LiteralHasPrefix is the deliberately-weaker sibling of f0LiteralExact, for
// the ONE shape where exact matching is impossible: VictoriaMetrics families
// built by concatenation, e.g.
//
//	`seasonfill_enrichment_refresh_total{tier="` + tier.String() + `",result="` + result + `"}`
//
// No single literal there ever equals the family name. Prefix matching anchored
// at the START of the literal is the strongest honest test available: it still
// rejects a rename (the prefix moves) and it still rejects a family mentioned
// mid-sentence in a prose literal, which plain Contains would accept.
//
// Use it ONLY for concatenated metric families, and say why at the call site —
// truthfully, about THAT literal. Its one caller today is the
// f0EnrichmentConcatFamilies loop in f0DetectEnrichmentMetrics, one family wide.
// The two siblings that used to ride along in that list are whole literals and
// now go through f0LiteralExact; a shared justification covering a mixed list is
// how a strict-looking detector goes soft.
// Everywhere else use f0LiteralExact.
func f0LiteralHasPrefix(t *testing.T, path, prefix string) bool {
	t.Helper()
	for _, lit := range f0StringLiterals(t, path) {
		if strings.HasPrefix(lit, prefix) {
			return true
		}
	}
	return false
}

// f0CallSpec addresses one callable that must actually be CALLED.
//
// This type is the core of B-46. The three earlier hardenings (F1 AST instead
// of raw text, F2 literal markers, F3 f0DetectSearchMetrics) all closed one
// instance of the same disease and left the mechanism intact: a detector that
// asks "is this name mentioned?" when the invariant is "is this code RUN?".
// The residual vector the operator named:
// internal/search/catalog/adapter.go carries eight a.observeGroup(...) call
// sites plus the helper that wraps observability.ObserveSearchGroup. Delete all
// eight calls, keep the helper — every identifier survives, the old detector
// stays true, and the per-entity search metrics go to zero.
//
// What a CallExpr actually proves is that the code is WRITTEN as a call — not
// that it executes. Reachability is not analysed: eight calls parked inside
// `if false { … }`, or in a method nothing ever invokes, still count (verified).
// Say it out loud rather than let the type read as stronger than it is. What the
// node does rule out is the whole false-Held family this file exists for:
// declarations, struct fields, interface methods and type names are not
// CallExprs, so none of them can satisfy a spec. "Mentioned" → "written as a
// call" is the step B-46 takes; "→ proven to run" needs a behavioural test, and
// where one exists (TestADR0025_F0_PickerBreakerBehaviour) it is cited.
type f0CallSpec struct {
	// Name is the EXACT callee name: Sel.Name for x.Foo(), Ident.Name for
	// Foo(). Never a substring.
	Name string
	// Receiver, when non-empty, requires the rendered receiver expression to
	// CONTAIN it — "w.deps.EnrichmentErrors" contains "EnrichmentErrors", so
	// `w.deps.EnrichmentErrors.RecordFailure(...)` matches while
	// `w.audit.RecordFailure(...)` does not. Substring is correct HERE
	// (unlike for Name) because the receiver path is an implementation
	// detail — the dependency it ends in is the invariant.
	Receiver string
	// StringArg, when non-empty, requires one argument to be a string
	// literal EQUAL to it. This is what turns DomainLogger(base, "watchdog")
	// into a miss for the "enrichment" cell.
	StringArg string
	// Min is the minimum number of matching calls. Zero reads as one. Set it
	// above one only where the COUNT itself is the claim the registry makes
	// — see f0SearchMetricCallSites.
	//
	// A Min above one is DELIBERATELY brittle, and the brittleness has a
	// correct response. Folding eight unrolled a.observeGroup(…) calls into
	// one loop is a legitimate refactor that drops the count to 1 and turns
	// this cell red. That failure is the mechanism working: the registry cell
	// claims "four entity groups × two arms are separately observed", and after
	// the refactor the source no longer says so statically. The fix is to
	// restate the claim — update Min here TOGETHER with the cell's Evidence in
	// internal/shared/verticals (which enumerates the call sites by line), so
	// the registry and the probe keep telling the same story. Do NOT drop Min
	// back to 1, delete the spec, or move the cell into f0Undetectable: each
	// silently converts "the split is real" into "something named observeGroup
	// exists", which is the exact false-Held B-46 closed.
	Min int
	// Why states the invariant this spec encodes, and is printed on failure.
	Why string
}

func (s f0CallSpec) min() int {
	if s.Min <= 0 {
		return 1
	}
	return s.Min
}

// String renders the spec the way it would be written in Go, for failure output.
func (s f0CallSpec) String() string {
	out := s.Name + "("
	if s.StringArg != "" {
		out += strconv.Quote(s.StringArg)
	}
	out += ")"
	if s.Receiver != "" {
		out = "<…" + s.Receiver + ">." + out
	}
	return out
}

// f0ExprString renders the receiver side of a call as source-like text. Only
// the shapes that can carry a receiver are handled; anything else renders empty
// and therefore never satisfies a non-empty f0CallSpec.Receiver.
func f0ExprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return f0ExprString(x.X) + "." + x.Sel.Name
	case *ast.ParenExpr:
		return f0ExprString(x.X)
	case *ast.StarExpr:
		return f0ExprString(x.X)
	case *ast.IndexExpr:
		return f0ExprString(x.X)
	case *ast.CallExpr:
		return f0ExprString(x.Fun) + "()"
	case *ast.BasicLit:
		return x.Value
	default:
		return ""
	}
}

// f0CountCalls counts the CallExpr nodes in one file that satisfy spec.
func f0CountCalls(t *testing.T, path string, spec f0CallSpec) int {
	t.Helper()
	require.NotEmptyf(t, spec.Name, "f0CallSpec without a callee name (%s)", path)

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "parse %s", path)

	n := 0
	ast.Inspect(f, func(nd ast.Node) bool {
		call, ok := nd.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, recv := f0CalleeName(call.Fun)
		if name != spec.Name {
			return true
		}
		if spec.Receiver != "" && !strings.Contains(recv, spec.Receiver) {
			return true
		}
		if spec.StringArg != "" && !f0CallHasStringArg(call, spec.StringArg) {
			return true
		}
		n++
		return true
	})
	return n
}

// f0CalleeName splits a call's Fun into (callee name, rendered receiver).
// Generic instantiations (IndexExpr / IndexListExpr) are unwrapped so that
// Foo[T]() counts as a call of Foo.
func f0CalleeName(fun ast.Expr) (name, recv string) {
	switch x := fun.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name, f0ExprString(x.X)
	case *ast.Ident:
		return x.Name, ""
	case *ast.IndexExpr:
		return f0CalleeName(x.X)
	case *ast.IndexListExpr:
		return f0CalleeName(x.X)
	default:
		return "", ""
	}
}

func f0CallHasStringArg(call *ast.CallExpr, want string) bool {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if v, err := strconv.Unquote(lit.Value); err == nil && v == want {
			return true
		}
	}
	return false
}

// f0HasCalls reports whether path carries at least spec.min() calls matching spec.
func f0HasCalls(t *testing.T, path string, spec f0CallSpec) bool {
	t.Helper()
	return f0CountCalls(t, path, spec) >= spec.min()
}

// ---------------------------------------------------------------------
// detectors — "does the code carry this invariant TODAY?"
// ---------------------------------------------------------------------

// f0JournalMarker is one requirement of the failure-journal invariant, tagged
// with the KIND of thing it is. Before B-46 all three markers went through the
// same substring-over-identifiers check, which silently flattened three
// different claims into one weak one — most damagingly for
// recordEnrichmentError, where the mere EXISTENCE of the helper satisfied a
// requirement that is really "the error path calls it", and for RecordFailure,
// which any object with a same-named method satisfied.
type f0JournalMarker struct {
	// Ident, when non-empty, must be USED as an identifier — the right test
	// for a dependency / field, which is referenced, not called.
	Ident string
	// Call, when its Name is non-empty, must actually be CALLED.
	Call f0CallSpec
	// Why states what the marker proves.
	Why string
}

// f0JournalWriteMarkers are the three things a worker must carry for its failure
// journal to be REAL rather than merely wired.
var f0JournalWriteMarkers = []f0JournalMarker{
	{
		Ident: "EnrichmentErrors",
		Why:   "the journal repository must be a DEPENDENCY of the worker",
	},
	{
		Call: f0CallSpec{Name: "recordEnrichmentError", Min: 1},
		Why: "the write helper must be CALLED from the error path — before B-46 its " +
			"mere declaration satisfied this marker, so deleting every call site " +
			"left the cell green with nothing ever written",
	},
	{
		Call: f0CallSpec{Name: "RecordFailure", Receiver: "EnrichmentErrors", Min: 1},
		Why: "the helper must call the repo write ON the journal dependency — a " +
			"RecordFailure on any other receiver (an audit sink, a local stub) is " +
			"not a journal write",
	},
}

// f0DetectFailureJournal reports whether ONE worker file carries an actual
// journal WRITE.
//
// How to confirm the probe still goes false (reviewer recipe, ~30 seconds):
// delete the three w.recordEnrichmentError(...) call sites in
// internal/enrichment/app/movie_worker.go:492,500,510 — leave the helper and
// every identifier in place — then run
// `go test -tags=integration ./tests/integration/ -run TestADR0025_F0_RegistryMatchesCode`.
// It must fail on failure_journal/movie with `declared "held", code carries it
// = false`. Before B-46 that edit left the suite GREEN. Restore afterwards.
func f0DetectFailureJournal(t *testing.T, root, rel string) bool {
	t.Helper()
	path := f0Path(t, root, rel)
	for _, m := range f0JournalWriteMarkers {
		if m.Ident != "" {
			if !f0IdentUsed(t, path, m.Ident) {
				return false
			}
			continue
		}
		if !f0HasCalls(t, path, m.Call) {
			return false
		}
	}
	return true
}

// f0StripSQLLineComments removes `-- …` to end of line. Go's parser hands the
// SQL back as one opaque literal, so SQL's own comment syntax has to be handled
// here or a commented-out gate counts as a live one — the same class of bug F1
// fixed for GO comments by switching to the AST.
func f0StripSQLLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if code, _, found := strings.Cut(line, "--"); found {
			lines[i] = code
		}
	}
	return strings.Join(lines, "\n")
}

// f0DetectPickerBreaker reports whether EVERY tier arm of a tiered picker
// carries the terminal-failure gate.
//
// B-46 changed the question from "are there at least as many gates as arms?" to
// "does each arm have one?". The old form was
//
//	strings.Count(sql, "ee.attempts >") >= strings.Count(sql, "UNION ALL")+1
//
// which a picker with three gates stacked in its first arm and none in the
// other four satisfied exactly — the arithmetic held while four of five tiers
// re-picked terminally-dead entities every tick, which IS the production
// incident (ADR-0025 Proof #2) this cell exists to prevent. Arms are now split
// apart and each one is required to carry the gate on its own. SQL `--`
// comments are stripped first, so a gate that survives only as documentation
// does not count.
//
// Two limits of a TEXT probe, named out loud rather than left to be rediscovered
// a fourth time:
//
//  1. Arms split across MORE THAN ONE string literal. Concatenating several
//     literals and splitting the result on "UNION ALL" would fabricate a seam
//     the SQL does not have — the tail of literal A and the head of literal B
//     would read as one arm and borrow each other's gate. Fixed rather than
//     documented: exactly ONE UNION ALL literal per file is required, and a
//     second one fails loudly with instructions. Both live pickers carry one
//     (series_refresh_query.go:155 holds all four UNION ALLs, movie:177 one).
//     The splinter this leaves, named so the next reader does not have to
//     rediscover it: ANY literal in the picker file containing "UNION ALL" —
//     a second unrelated query, an example inside a doc string that the parser
//     hands back as a literal — trips the count and turns the cell RED although
//     nothing regressed. That direction is the safe one (a false red is
//     investigated, a false green is not), and the fix when it happens is to
//     teach this probe to SELECT the picker's query literal (by the function it
//     is returned from, say) — never to relax the count back to a >= that lets
//     two literals be glued together.
//
//  2. `ee.attempts >` in the SELECT LIST instead of the WHERE clause —
//     `SELECT …, (ee.attempts > 5) AS is_dead` gates nothing and counts here.
//     No text probe can tell a projection from a predicate; that needs a
//     parser or an execution. The behavioural cover is
//     TestADR0025_F0_PickerBreakerBehaviour, which runs the real picker against
//     a terminally-failed row and asserts it is not returned — real coverage,
//     but Postgres-only, so it SKIPS wherever Docker is unavailable (CI has it,
//     a laptop may not). This static probe is what runs everywhere.
func f0DetectPickerBreaker(t *testing.T, root, rel string) bool {
	t.Helper()
	path := f0Path(t, root, rel)

	var armLits []string
	for _, lit := range f0StringLiterals(t, path) {
		if strings.Contains(lit, "UNION ALL") {
			armLits = append(armLits, lit)
		}
	}
	require.NotEmptyf(t, armLits, "no UNION ALL SQL literal found in %s — the detector "+
		"would silently pass; the picker was probably restructured", path)
	require.Lenf(t, armLits, 1,
		"%s carries %d separate string literals containing UNION ALL. This probe splits ONE "+
			"literal on UNION ALL to get the tier arms; gluing several together would invent a "+
			"seam the SQL does not have, and the two halves either side of it would borrow each "+
			"other's terminal gate — a false Held. Failing loudly instead. If the picker was "+
			"deliberately split across literals, teach this detector to walk each literal's arms "+
			"separately; do NOT concatenate them.", path, len(armLits))

	sql := f0StripSQLLineComments(armLits[0])
	require.Containsf(t, sql, "UNION ALL",
		"every UNION ALL in %s sits inside a `--` SQL comment — the arm split would be "+
			"meaningless and the detector would report one big always-gated arm", path)

	for i, arm := range strings.Split(sql, "UNION ALL") {
		if !strings.Contains(arm, "ee.attempts >") {
			t.Logf("%s: tier arm #%d carries no `ee.attempts >` terminal gate", path, i)
			return false
		}
	}
	return true
}

// f0RetrySweepSources returns the Source constant names handed to
// ListDueForRetry by the wiring file at rel.
//
// rel is a parameter rather than f0RelWiringEnrichment spelled inside, because
// the cell that uses this helper must be able to hand it c.Reads[0] — see the
// probe contract on f0Cell. A detector that names its own file gives the cell's
// Reads declaration nothing to be true about.
func f0RetrySweepSources(t *testing.T, root, rel string) []string {
	t.Helper()
	path := f0Path(t, root, rel)
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

// f0RetrySweepHasSource reports whether the nightly wiring hands the EXACT
// Source constant to ListDueForRetry.
//
// B-46: the movie arm used strings.Contains(s, "Movie"), so a
// SourceTMDBMovieLegacy or SourceMovieDiscovery arm would have satisfied the
// movie retry-sweep cell while SourceTMDBMovie — the one the journal writes —
// was swept by nobody.
func f0RetrySweepHasSource(t *testing.T, root, rel, want string) bool {
	t.Helper()
	for _, s := range f0RetrySweepSources(t, root, rel) {
		if s == want {
			return true
		}
	}
	return false
}

// f0GrabRecordFields returns the field names of grab.Record as declared in the
// file at rel. rel is a parameter for the same reason as in
// f0RetrySweepSources: the cell hands it c.Reads[0].
func f0GrabRecordFields(t *testing.T, root, rel string) []string {
	t.Helper()
	path := f0Path(t, root, rel)
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

// f0MetricSite is one production file that must carry real increment CALLS,
// together with the specs it must satisfy. A metric family nobody increments on
// a hot path is not observability — it is a string constant.
type f0MetricSite struct {
	// rel is the repo-relative slash path of the production file. One string,
	// not a path fragment list, so f0SiteRels can hand the very same values to
	// an f0Cell.Reads declaration — the probe and the declaration are then
	// literally the same data, not two copies that can drift.
	rel   string
	specs []f0CallSpec
}

// f0SiteRels returns the repo-relative paths a site list reads, for use as an
// f0Cell.Reads declaration.
func f0SiteRels(sites []f0MetricSite) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.rel)
	}
	return out
}

// f0MetricSitesSatisfied reports whether every site carries every one of its
// specs. A missing file is a clean false, not a failure: a fully reverted phase
// must read as "Gap", not as a broken test.
//
// rels are the paths the CELL declared it reads for these sites, and the files
// actually opened are rels[i] — not site.rel. The two must be the same list, and
// that is asserted, not assumed: without the assertion a multi-file probe would
// be the one shape the B-46 probe contract cannot reach (the paths would come
// from a package-level site list instead of from the cell), which is exactly the
// H-1 hole the second review reproduced. Drift fails LOUDLY rather than
// returning false, because "the declaration and the probe disagree about which
// files this invariant lives in" is a broken test, not a Gap.
//
// This assertion is NOT made redundant by the opened-set rule (f0RunProbe), and
// it is kept deliberately. The opened-set rule compares what f0Path resolved
// with what the cell declared; both of those come from rels. Nothing in it ties
// sites[i].specs — which decide WHAT each file must carry — to rels[i], which
// decides WHICH file it is checked against. Drop this line and a site list one
// entry longer than the declaration would silently apply each spec to the wrong
// file (or panic on an index), with the opened set still perfectly equal.
//
// paths are the already-resolved absolute paths for rels, resolved by the caller
// BEFORE any short-circuit, so a probe that gives up on the first missing site
// has still registered its whole declared read set. See f0Paths.
func f0MetricSitesSatisfied(t *testing.T, paths, rels []string, sites []f0MetricSite) bool {
	t.Helper()
	require.Equalf(t, f0SiteRels(sites), rels,
		"the cell's declared Reads and this site list have drifted apart. The probe opens "+
			"what the CELL declares, so a site list the declaration does not name would be "+
			"probed on paper only; re-derive Reads from the site list (f0SiteRels) instead of "+
			"maintaining a second copy")
	require.Lenf(t, paths, len(rels),
		"resolved %d paths for %d declared reads — the caller resolved a different list "+
			"than it declared", len(paths), len(rels))
	for i, site := range sites {
		path := paths[i]
		if _, err := os.Stat(path); err != nil {
			return false
		}
		for _, spec := range site.specs {
			if !f0HasCalls(t, path, spec) {
				t.Logf("%s: missing %s (min %d) — %s", path, spec, spec.min(), spec.Why)
				return false
			}
		}
	}
	return true
}

// f0SearchMetricFamilies are the four seasonfill_search_* family names that must
// exist as EXACT string literals in internal/observability/search_metrics.go.
var f0SearchMetricFamilies = []string{
	"seasonfill_search_requests_total",
	"seasonfill_search_request_duration_seconds",
	"seasonfill_search_group_queries_total",
	"seasonfill_search_group_duration_seconds",
}

// f0SearchMetricCallSites are the production files that must INCREMENT, with the
// minimum call counts the registry itself already asserts.
//
// The counts are not decoration. The metrics_namespace/movie Evidence string
// enumerates the call sites line by line — usecase.go:98,107,116,125 and
// adapter.go:94,99,108,113,122,127,136,141 — because the PER-ENTITY split is the
// whole claim of ADR-0025 F3: ADR-0024 BUG-2 was a 12-second PEOPLE query hidden
// inside requests whose other three groups answered in ~100ms. Four entity
// groups in the use case, four groups × two (error / success) arms in the
// adapter. Requiring only "at least one" would let seven of the eight adapter
// arms be deleted with the cell still green, and the exact incident class the
// families exist to catch would go back to being invisible.
var f0SearchMetricCallSites = []f0MetricSite{
	{
		rel: "internal/search/rest/handler.go",
		specs: []f0CallSpec{{
			Name: "ObserveSearchRequest", Receiver: "observability", Min: 1,
			Why: "the HTTP boundary must record request count and latency",
		}},
	},
	{
		rel: "internal/search/app/usecase.go",
		specs: []f0CallSpec{
			{
				Name: "ObserveSearchGroup", Receiver: "observability", Min: 1,
				Why: "the library-scope helper must reach the metric",
			},
			{
				Name: "observeGroup", Min: 4,
				Why: "one per entity group (series, movies, collections, people) — " +
					"fewer means a group's latency is no longer separable",
			},
		},
	},
	{
		rel: "internal/search/catalog/adapter.go",
		specs: []f0CallSpec{
			{
				Name: "ObserveSearchGroup", Receiver: "observability", Min: 1,
				Why: "the catalog-scope helper must reach the metric",
			},
			{
				Name: "observeGroup", Min: 8,
				Why: "four entity groups × the error and success arm of each — this is " +
					"the exact vector B-46 closes: deleting all eight and keeping the " +
					"helper left every identifier in place and the old detector green",
			},
		},
	},
}

// f0DetectSearchMetrics reports whether the search bounded context REALLY
// exports metrics.
//
// How to confirm the probe still goes false (reviewer recipe, ~30 seconds):
// delete the eight a.observeGroup(...) call sites in
// internal/search/catalog/adapter.go:94,99,108,113,122,127,136,141 and LEAVE the
// Adapter.observeGroup helper at :160 in place, then run
// `go test -tags=integration ./tests/integration/ -run TestADR0025_F0_RegistryMatchesCode`.
// It must fail on metrics_namespace/movie. Before B-46 that edit left the suite
// GREEN while the per-entity catalog series went to zero. Restore afterwards.
//
// Do the deletion with an editor or a python one-liner, NOT with BSD `sed` and a
// `\b` word boundary: macOS sed does not understand `\b`, the pattern matches
// nothing, every call site survives untouched and the recipe reports a FALSE
// GREEN that reads as "the detector is fine". Review #4 walked into exactly that
// before redoing the edit in python and getting the red.
func f0DetectSearchMetrics(t *testing.T, root string, reads []string) bool {
	t.Helper()
	require.Equalf(t, f0SearchMetricReads(), reads,
		"this probe opens the paths the CELL declares; the list it was handed is not the "+
			"search-metrics read set")
	// Resolve the WHOLE declared read set before the short-circuits below, so
	// the opened-set rule (f0RunProbe) sees the declaration this probe committed
	// to rather than however far it happened to get.
	paths := f0Paths(t, root, reads)
	if _, err := os.Stat(paths[0]); err != nil {
		// The whole family was removed. Report a clean false rather than
		// failing the parse — a reverted F3 must read as "Gap", not as a
		// broken test.
		return false
	}
	for _, family := range f0SearchMetricFamilies {
		if !f0LiteralExact(t, paths[0], family) {
			return false
		}
	}
	return f0MetricSitesSatisfied(t, paths[1:], reads[1:], f0SearchMetricCallSites)
}

// f0SearchMetricReads is the read set of the metrics_namespace/movie cell: the
// family declaration file first, then one entry per call site. Spelled once, so
// the cell's Reads and the probe's paths are the same value rather than two
// copies — and so the adversarial suite can hand the probe the very list the
// cell does.
func f0SearchMetricReads() []string {
	return append([]string{f0RelSearchMetrics}, f0SiteRels(f0SearchMetricCallSites)...)
}

// f0EnrichmentConcatFamilies is the ONE enrichment family that no single string
// literal can ever equal, because the label set is glued on at call time
// (internal/observability/enrichment_refresh_metrics.go:27):
//
//	`seasonfill_enrichment_refresh_total{tier="` + tier.String() + `",result="` + result + `"}`
//
// Prefix-anchored matching is the strongest honest test available for THIS
// shape and no other — see f0LiteralHasPrefix.
var f0EnrichmentConcatFamilies = []string{
	"seasonfill_enrichment_refresh_total{",
}

// f0EnrichmentExactFamilies are the enrichment families written as WHOLE
// literals — enrichment_refresh_metrics.go:35 and :42 hand them to
// GetOrCreateGauge / GetOrCreateHistogram unconcatenated — so they get
// f0LiteralExact like every other name in this file.
//
// B-46 review caught these two riding along in the prefix list under a call-site
// note claiming "these three are built by concatenation". Two of the three were
// not, and the untrue justification cost real strictness: renaming
// ..._batch_size → ..._batch_size_v2 and ..._tick_seconds → ..._tick_seconds_v2
// in the live adapter left metrics_namespace/series GREEN (reproduced on the
// real file, before/after in the B-46 report) — the identical
// near-miss-rename hole B-46 had just closed for the search families, one
// variable away. A prose literal that merely STARTS with a family name passed
// too. Both are red now; f0advEnrichMetricsNearMiss* pins them.
var f0EnrichmentExactFamilies = []string{
	"seasonfill_enrichment_refresh_batch_size",
	"seasonfill_enrichment_refresh_tick_seconds",
}

// f0EnrichmentMetricCallSites are the three files that make the enrichment
// families real: the adapter that creates the VictoriaMetrics series, the
// scheduler hot path that increments them, and the wiring that binds the real
// adapter instead of app.noopRefreshMetrics.
//
// The wiring site is not redundant. internal/enrichment/app/refresh_scheduler.go
// defaults Metrics to a no-op implementation whose method set is identical; if
// internal/wiring/enrichment.go stopped calling
// observability.NewEnrichmentRefreshMetrics(), every call site above would still
// be there, every literal would still be there, and /metrics would export
// nothing.
var f0EnrichmentMetricCallSites = []f0MetricSite{
	{
		rel: f0RelEnrichMetrics,
		specs: []f0CallSpec{
			{Name: "GetOrCreateCounter", Receiver: "metrics", Min: 1, Why: "the counter family must be created"},
			{Name: "GetOrCreateGauge", Receiver: "metrics", Min: 1, Why: "the gauge family must be created"},
			{Name: "GetOrCreateHistogram", Receiver: "metrics", Min: 1, Why: "the histogram family must be created"},
		},
	},
	{
		rel: "internal/enrichment/app/refresh_scheduler.go",
		specs: []f0CallSpec{
			{
				Name: "IncRefresh", Receiver: "Metrics", Min: 3,
				Why: "one per outcome the tick can produce (ok / skipped / error) — " +
					"the result label is useless if only one arm ever increments",
			},
			{Name: "ObserveBatchSize", Receiver: "Metrics", Min: 1, Why: "batch size must be recorded per tick"},
			{Name: "ObserveTickDuration", Receiver: "Metrics", Min: 1, Why: "tick latency must be recorded"},
		},
	},
	{
		rel: f0RelWiringEnrichment,
		specs: []f0CallSpec{{
			Name: "NewEnrichmentRefreshMetrics", Receiver: "observability", Min: 1,
			Why: "the real adapter must be wired — the scheduler silently defaults to " +
				"noopRefreshMetrics, which satisfies every call site above and exports nothing",
		}},
	},
}

// f0DetectEnrichmentMetrics reports whether the enrichment bounded context
// REALLY exports metrics.
//
// This replaces f0ObservabilityHasPrefix(t, root, "seasonfill_enrichment_"),
// which B-46 deleted. That probe walked the WHOLE internal/observability/
// directory and returned true on the first literal containing the prefix in any
// production file — the exact shape ADR-0025 F3 proved false-Held by experiment
// for the search families (live metrics file kept, every increment deleted, cell
// still green). F3 fixed the search cell and left the identical weakness in
// place for the enrichment cell, which is the pattern B-46 exists to stop:
// closing one instance of a class and leaving its twin.
//
// How to confirm the probe still goes false (reviewer recipe, ~30 seconds):
// delete the three s.deps.Metrics.IncRefresh(...) call sites in
// internal/enrichment/app/refresh_scheduler.go:239,244,247 — leave the
// RefreshMetrics interface, the adapter and every literal in place — then run
// `go test -tags=integration ./tests/integration/ -run TestADR0025_F0_RegistryMatchesCode`.
// It must fail on metrics_namespace/series. Restore afterwards.
func f0DetectEnrichmentMetrics(t *testing.T, root string, reads []string) bool {
	t.Helper()
	require.Equalf(t, f0EnrichmentMetricReads(), reads,
		"this probe opens the paths the CELL declares; the list it was handed is not the "+
			"enrichment-metrics read set")
	// Same reason as f0DetectSearchMetrics: whole read set resolved up front.
	paths := f0Paths(t, root, reads)
	if _, err := os.Stat(paths[0]); err != nil {
		return false
	}
	for _, family := range f0EnrichmentConcatFamilies {
		// Prefix, and ONLY here: this family name never exists as a whole
		// literal because the labels are concatenated onto it at call time.
		if !f0LiteralHasPrefix(t, paths[0], family) {
			return false
		}
	}
	for _, family := range f0EnrichmentExactFamilies {
		// Whole literals in the adapter, so the same exact test the search
		// families get. Prefix matching here would accept a _v2 rename.
		if !f0LiteralExact(t, paths[0], family) {
			return false
		}
	}
	return f0MetricSitesSatisfied(t, paths, reads, f0EnrichmentMetricCallSites)
}

// f0EnrichmentMetricReads is the read set of the metrics_namespace/series cell.
// Its first entry is the adapter that declares the families
// (f0EnrichmentMetricCallSites[0].rel == f0RelEnrichMetrics), so unlike the
// search set it needs no extra head element. Same purpose as
// f0SearchMetricReads: one value shared by the declaration and the probe.
func f0EnrichmentMetricReads() []string {
	return f0SiteRels(f0EnrichmentMetricCallSites)
}

// f0DomainAllowed reports whether domain is in the closed AllowedDomains list.
func f0DomainAllowed(domain string) bool {
	_, ok := sharedports.AllowedDomains[domain]
	return ok
}

// f0DetectDomainLogger reports whether ONE worker actually logs under domain.
//
// B-46: before this change domain_logger/series and domain_logger/movie ran the
// IDENTICAL probe — `_, ok := sharedports.AllowedDomains["enrichment"]` — with
// no reference to the vertical at all. Two registry cells sharing one
// vertical-blind detector means one of them asserts nothing: delete the
// DomainLogger call from movie_worker.go and the movie cell stays green on the
// strength of series' wiring. The half that varies per vertical (the worker
// actually calling DomainLogger with that domain) was the half nobody checked,
// even though the cell's own Evidence cites movie_worker.go by line.
//
// Both halves are required now: the domain must be in the closed list AND the
// vertical's worker must call DomainLogger with it.
func f0DetectDomainLogger(t *testing.T, root, domain, rel string) bool {
	t.Helper()
	// Resolved BEFORE the closed-list gate: the cell declared this file, so it
	// must register as opened even when the probe can answer "no" without
	// parsing it. Otherwise a legitimate Gap would look like a probe that
	// ignored its own declaration (see f0Paths / f0RunProbe).
	path := f0Path(t, root, rel)
	if !f0DomainAllowed(domain) {
		return false
	}
	return f0HasCalls(t, path, f0CallSpec{
		Name: "DomainLogger", StringArg: domain, Min: 1,
		Why: "the vertical's worker must wire its logger through the closed domain list",
	})
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

// f0Cell binds one Held/Gap registry cell to its probe AND to the source it is
// allowed to look at.
//
// Why the extra structure (B-46 review finding B-2). Before this, f0Detectors
// was map[Key]f0Probe — a bare closure per cell — and the rule that kept a cell
// pointed at its OWN vertical's code was written in PROSE above the map. Prose
// does not fail a build. Reproduced by the reviewer: move the failure_journal/
// movie closure onto series_worker.go and the movie cell reports Held with
// movie_worker.go completely gutted. Neither the conformance test nor the
// adversarial selftest caught it, because both exercise the detector FUNCTIONS
// and nothing exercised their BINDING to cells. That is precisely the bug B-46
// had just fixed for domain_logger, one level up — the class returning for a
// fourth time from the side nobody was watching.
//
// Reads, Discriminator and Markers turn the prose into data — but a struct on
// its own changes nothing, and the second B-46 review proved it: the FIRST
// version of this type shipped with a docblock claiming the closure "has no
// other way to name a file or a constant", while `Probe` was an ordinary
// closure over the whole package. The reviewer left Reads honest, changed the
// body's single argument from c.Reads[0] to f0RelSeriesWorker, gutted
// movie_worker.go — and the suite stayed green. A second vector: give two cells
// reading ONE file two different discriminators that neither body mentions;
// f0BindingViolations was satisfied by the mere PRESENCE of the strings, so the
// lock was walked around rather than picked. A docblock that overstates its own
// mechanism is the exact disease this file treats, one level up.
//
// So the claim is now carried by a mechanism: f0ProbeContractViolations parses
// THIS file (runtime.Caller gives the suite its own source) and holds every
// Probe body in the f0Detectors literal to a contract —
//
//   - no string literal anywhere in the body;
//   - no package-level const or var (so f0RelSeriesWorker, the family lists and
//     the site lists are all out of reach, whatever they are renamed to);
//   - no qualified identifier from another package (a string constant elsewhere
//     is a path or a marker in disguise);
//   - c.Reads must be consumed, and Discriminator / Markers must be consumed if
//     and only if they are declared.
//
// The only remaining source of a path or a token WRITTEN inside a probe is
// therefore the cell it was handed. That was as far as the second review got,
// and the docblock used to stop there claiming a mis-binding "has to be written
// into the DECLARATION". It did not: the third review showed the contract is
// satisfied by taking a path from the cell and taking the WRONG ONE — declare
// Reads: {movie_worker, series_worker} and open c.Reads[1]. The declaration
// reads as an ordinary two-file cell, c.Reads is consumed, the binding rule
// finds the movie token in a read path, and movie_worker.go can be gutted while
// the registry says Held. Adding a file to Reads is a routine edit; the class
// survived one token.
//
// So the syntactic half is now paired with a BEHAVIOURAL one: f0Path, the single
// funnel that turns a declared rel into an absolute path, records what it was
// actually handed while a probe runs, and f0RunProbe requires the opened set to
// EQUAL c.Reads — equality in both directions. Be exact about what that buys,
// because the fourth review caught THIS docblock overstating it. f0Path records
// the RESOLUTION of a rel, not a read of the bytes, so the equality rule catches
// two things: a probe that resolves a path the cell never declared, and a probe
// that LAZILY skips a declared path it decided it did not need. For a
// single-file cell that is the whole space, and "the declaration is what the
// probe reads" is a fact there.
//
// For a MULTI-FILE cell it is still a promise, and deliberately so. Multi-file
// probes are REQUIRED to resolve their whole read set up front (f0Paths) so that
// an honest short-circuit cannot register a partial set and fire a false red —
// and that same prescription makes opened ≡ declared identically true for them,
// which leaves the equality rule nothing to say about WHICH index the body then
// used. So the one-token index swap (attack D) is not caught inside a probe that
// follows the prescription. That is a chosen trade — freedom from false reds on
// every honest short-circuiting probe, bought with index-swap detection in the
// multi-file shape — and it is carried in the remainder list below and in a
// named case in TestADR0025_F0_ProbeOpenSetBites, not papered over here.
// f0MetricSitesSatisfied still ties each site's SPECS to the declared path at
// the same index.
//
// What this still does not prove, said out loud rather than left to a fifth
// discovery:
//
//   - a path can be declared, resolved, and its RESULT ignored — `f0Path` records
//     an intent to read, not a use of the bytes. Equality of the opened set makes
//     a LAZILY skipped declared path loud; it does not make a deliberately inert
//     probe impossible.
//   - the one-token index swap SURVIVES inside a multi-file probe that follows
//     the f0Paths prescription, and the prescription is precisely what lets it
//     survive: resolving all of Reads up front makes opened ≡ declared
//     identically true, so the equality rule is a tautology for that probe.
//     Review #4 reproduced it on the live tree — `f0Paths(t, root, c.Reads)`
//     followed by `f0DetectFailureJournal(t, root, c.Reads[1])`, three
//     w.recordEnrichmentError call sites removed from movie_worker.go,
//     failure_journal/movie reporting Held, the whole suite ok. The same shape
//     appears when a two-file cell loses one conjunct of its `&&` and nobody
//     trims Reads. It is LATENT rather than open today only because no cell is a
//     hand-written multi-file cell: the two that exist
//     (metrics_namespace/series, metrics_namespace/movie) derive Reads from
//     their site lists and are pinned by f0MetricSitesSatisfied's
//     `require.Equalf(f0SiteRels(sites), rels)`, which binds every spec to the
//     path at its own index. The first hand-written multi-file cell re-opens
//     this; no mechanism here will notice, so the reviewer of that diff must.
//   - a body can consume c.Discriminator VACUOUSLY. The blank-assignment form
//     (`_ = c.Discriminator`), which review #3 used to satisfy the consumption
//     rule while telling two cells on one foreign file apart with pure
//     decoration, is now itself a violation. The remainder is narrower than this
//     bullet used to claim: `… && c.Discriminator != ""` is NOT an example of
//     it, because `""` is a string literal and the no-literal rule rejects that
//     body outright. The spelling that still passes is the literal-free one,
//     `… && len(c.Discriminator) > 0`. Consumption is syntactic; what the token
//     is used FOR is the reviewer's job.
//   - the damage that vector actually did — two cells where only a FOREIGN
//     vertical's file is read — is closed from the other side by rule 2-bis in
//     f0BindingViolations, which does not consult the Discriminator at all. With
//     one exception, said out loud rather than left implied: 2-bis is INERT for
//     the retry_sweep pair. Both of its cells read internal/wiring/enrichment.go,
//     a path that carries no vertical token at all, so there is no foreign token
//     for 2-bis to fire on and no own token it can find missing. For that pair
//     the discriminator is the whole binding, and all that holds it is the
//     syntactic consumption rule plus the no-duplicate-(reads, discriminator)
//     check: a body that consumed SourceTMDBMovie vacuously and probed for
//     something else would be caught by nothing in this file.
//   - the Discriminator is not validated against the thing it names: nothing
//     checks that "SourceTMDBMovie" is a real enrichment Source constant, only
//     that the probe uses the string. A typo'd discriminator makes the cell go
//     red, not silently green, so this is the safe direction — named here so the
//     next reader does not mistake it for coverage.
//   - a body could call an f0 helper that hardcodes a path inside ITSELF. That is
//     why every detector helper now takes its rel as a parameter
//     (f0DetectFailureJournal, f0DetectPickerBreaker, f0DetectDomainLogger,
//     f0RetrySweepSources, f0GrabRecordFields) and the two multi-file probes
//     assert their reads against the cell's: there is no helper left that knows a
//     path of its own. Such a helper would now ALSO have to route its path
//     through f0Path, where the opened-set rule would see a path the cell never
//     declared — so it is a new FUNCTION in the diff that fails loudly, not a
//     one-token swap inside a closure that passes.
//   - the no-string-literal rule is blunt: it also forbids a body from calling a
//     stdlib function that happens to need a literal. In practice every probe
//     body is one call, so this has cost nothing; the escape valve is to put the
//     string in Markers or to write the helper outside the map literal.
//
// The rule removes the ways a probe can silently look elsewhere, and now the
// ways it can silently look at only part of what it declared; it does not remove
// the ability to write a deliberately meaningless probe in plain sight.
type f0Cell struct {
	// Reads are the repo-relative slash paths this cell's probe opens — the
	// f0Rel* constants, never a fresh literal. The probe receives the cell
	// and reads Reads[i]; declaration and behaviour are one value.
	Reads []string
	// Discriminator is the EXACT constant, field or literal that makes this
	// cell about THIS vertical when the file paths do not. Empty means "the
	// paths carry it". The probe must consume it (enforced), so it is not a
	// label: rebinding retry_sweep/movie to SourceTMDBSeries changes the
	// probe's behaviour AND collides with the series cell's discriminator.
	Discriminator string
	// Markers are the other exact tokens the probe needs — a log domain, a
	// co-required struct field — which do NOT distinguish the vertical and so
	// do not belong in Discriminator. They live here because the contract
	// forbids literals inside the body: a token nobody can read from the
	// declaration is a token nobody reviews.
	Markers []string
	// Probe answers the Held/Gap question using only c (and root, to turn
	// c.Reads into absolute paths).
	Probe func(t *testing.T, root string, c f0Cell) bool
}

// f0RunProbe runs one cell's probe with f0Path recording turned on, and returns
// the probe's answer together with the repo-relative paths it actually resolved.
//
// Every caller of a Probe goes through here — the conformance test, the deferred
// guards, and the adversarial suite — so the opened-set rule cannot be bypassed
// by calling cell.Probe directly and forgetting the check.
func f0RunProbe(t *testing.T, root string, c f0Cell) (bool, []string) {
	t.Helper()
	stop := f0TrackOpens(t)
	held := c.Probe(t, root, c)
	return held, stop()
}

// f0ProbeOpenViolations compares what a cell DECLARED it reads with what its
// probe actually opened, and returns one line per mismatch. Empty means the
// declaration and the behaviour are the same thing.
//
// Equality, not containment, and both halves matter:
//
//   - opened ⊄ declared is a probe reading a file nobody can review from the
//     declaration — the original H-1 shape, now caught behaviourally as well as
//     syntactically;
//   - declared ⊄ opened is a declared path the body never resolved at all —
//     review #3's attack D in its LAZY form: a second path put on Reads purely to
//     satisfy the binding rule's vertical-token check, and a body that reads the
//     other index and never touches this one. Without this direction that path is
//     free costume.
//
// The limit of that second direction, stated here because review #4 found this
// docblock implying more than the code does: putting a file on Reads and then
// RESOLVING it up front while reading the other one still costs nothing. Probes
// resolve their whole declared read set up front (f0Paths) so that "I gave up
// early" never produces a partial set and never a false red — and that same
// prescription makes the opened set equal the declaration whichever index the
// body then used. So this rule catches attack D in its lazy form only; the eager
// form is the trade named in the f0Cell remainder list, latent today because
// every multi-file cell derives its Reads from a site list pinned by
// f0MetricSitesSatisfied. If a future probe genuinely cannot resolve a path
// before deciding, resolve it anyway — f0Path is a string operation — and know
// that doing so buys short-circuit freedom at the price of index-swap detection
// in that cell.
func f0ProbeOpenViolations(label string, declared, opened []string) []string {
	openedSet := map[string]bool{}
	for _, rel := range opened {
		openedSet[rel] = true
	}
	declaredSet := map[string]bool{}
	for _, rel := range declared {
		declaredSet[rel] = true
	}

	var out []string
	for _, rel := range declared {
		if !openedSet[rel] {
			out = append(out, label+": declares it reads "+rel+" but the probe never opened "+
				"it. A path on Reads that nothing opens is a costume: it satisfies the "+
				"binding rule's vertical-token check for free while the body reads some "+
				"OTHER declared path (B-46 review #3, attack D — two honest-looking reads, "+
				"c.Reads[1] in the body, the vertical's own file gutted and the cell green)")
		}
	}
	for _, rel := range opened {
		if !declaredSet[rel] {
			out = append(out, label+": the probe opened "+rel+", which the cell does not "+
				"declare in Reads. Every file a probe reads must be visible in the "+
				"declaration, or a reviewer is reading a different test than the one that runs")
		}
	}
	if len(declared) > 0 && len(opened) == 0 {
		out = append(out, label+": the probe opened no file at all — it cannot be asserting "+
			"anything about the source tree")
	}
	sort.Strings(out)
	return out
}

// f0Detectors maps a Held/Gap cell to its probe and its read set.
//
// B-46 rule, now mechanical and in TWO halves, both asserted by
// TestADR0025_F0_DetectorBindingIsVerticalSpecific:
//
//   - f0BindingViolations reads the DECLARATIONS: every cell must be
//     identifiable as belonging to its own vertical — the vertical's name in a
//     read path or in the discriminator — and no two cells of one invariant may
//     declare the same reads AND the same discriminator.
//   - f0ProbeContractViolations reads the BODIES, because the first half alone
//     was walked around twice in review: a body may take its paths and its
//     tokens ONLY from the cell it is handed (no literals, no package-level
//     values, no constants from other packages), and must consume what the cell
//     declares.
//
// Each half is useless without the other. Declarations that nothing is forced
// to use are labels; bodies whose inputs come from the cell tell you nothing
// unless the cells are distinguishable. domain_logger/series and
// domain_logger/movie once ran the identical vertical-blind closure; whichever
// of the two was wrong, the pair stayed green.
var f0Detectors = map[verticals.Key]f0Cell{
	{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalSeries}: {
		Reads: []string{f0RelSeriesWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
	{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalMovie}: {
		// movie_ports.go is deliberately NOT in Reads: a port declaration is
		// a dependency, not a write.
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
	{Invariant: verticals.InvariantPickerBreaker, Vertical: verticals.VerticalSeries}: {
		Reads: []string{f0RelSeriesPicker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectPickerBreaker(t, root, c.Reads[0])
		},
	},
	{Invariant: verticals.InvariantPickerBreaker, Vertical: verticals.VerticalMovie}: {
		Reads: []string{f0RelMoviePicker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectPickerBreaker(t, root, c.Reads[0])
		},
	},
	{Invariant: verticals.InvariantRetrySweep, Vertical: verticals.VerticalSeries}: {
		// Both retry-sweep cells read the one wiring file, so the path cannot
		// tell them apart — the Source constant is the whole discriminator,
		// and it is declared here rather than buried in the closure.
		Reads:         []string{f0RelWiringEnrichment},
		Discriminator: "SourceTMDBSeries",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0RetrySweepHasSource(t, root, c.Reads[0], c.Discriminator)
		},
	},
	{Invariant: verticals.InvariantRetrySweep, Vertical: verticals.VerticalMovie}: {
		// EXACT constant, not strings.Contains(s, "Movie"): the journal writes
		// SourceTMDBMovie, so a sweep of any other Movie-ish source would leave
		// this journal write-only while the cell reported Held.
		Reads:         []string{f0RelWiringEnrichment},
		Discriminator: "SourceTMDBMovie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0RetrySweepHasSource(t, root, c.Reads[0], c.Discriminator)
		},
	},
	{Invariant: verticals.InvariantLoopDeclaresTypes, Vertical: verticals.VerticalMovie}: {
		// ADR-0025 F2 names the marker verbatim: "one INFO
		// regrab_skipped_unsupported_type per swap instead of a WARN every
		// 30 minutes". EXACT literal — the plural
		// "regrab_skipped_unsupported_types" is a plausible rename that would
		// break every dashboard and log filter built on the singular, and
		// strings.Contains accepted it.
		Reads:         []string{f0RelRegrabLoop},
		Discriminator: "regrab_skipped_unsupported_type",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0LiteralExact(t, f0Path(t, root, c.Reads[0]), c.Discriminator)
		},
	},
	{Invariant: verticals.InvariantRegrabSupported, Vertical: verticals.VerticalSeries}: {
		// grab.Record is one shared type, so the discriminator is the field
		// that makes a grab a SERIES grab. SeasonNumber is required too, but
		// it does not discriminate the vertical — it is a Marker, declared
		// here because the probe contract forbids literals in the body.
		Reads:         []string{f0RelGrabRecord},
		Discriminator: "SeriesID",
		Markers:       []string{"SeasonNumber"},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			var hasSeries, hasSeason bool
			for _, f := range f0GrabRecordFields(t, root, c.Reads[0]) {
				switch f {
				case c.Discriminator:
					hasSeries = true
				case c.Markers[0]:
					hasSeason = true
				}
			}
			return hasSeries && hasSeason
		},
	},
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalSeries}: {
		// B-46: was f0ObservabilityHasPrefix(t, root, "seasonfill_enrichment_"),
		// a directory-wide literal scan with no increment check — the exact
		// probe F3 proved false-Held by experiment for the search families and
		// then left standing here. See f0DetectEnrichmentMetrics.
		Reads: f0EnrichmentMetricReads(),
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectEnrichmentMetrics(t, root, c.Reads)
		},
	},
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalMovie}: {
		// Subject = search bounded context (see the Status.Subject note on
		// this cell): the movie vertical already exported metrics, the search
		// bc did not until F3.
		Reads: f0SearchMetricReads(),
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectSearchMetrics(t, root, c.Reads)
		},
	},
	{Invariant: verticals.InvariantDomainLogger, Vertical: verticals.VerticalSeries}: {
		// The domain is shared by both cells, so it is a Marker, not the
		// Discriminator — the FILE is what makes this cell the series one.
		Reads:   []string{f0RelSeriesWorker},
		Markers: []string{"enrichment"},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectDomainLogger(t, root, c.Markers[0], c.Reads[0])
		},
	},
	{Invariant: verticals.InvariantDomainLogger, Vertical: verticals.VerticalMovie}: {
		// Distinct from the series cell above by declared FILE, not only by
		// comment: the cell's Evidence cites movie_worker.go, so the probe
		// must read movie_worker.go — and now it cannot read anything else.
		Reads:   []string{f0RelMovieWorker},
		Markers: []string{"enrichment"},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectDomainLogger(t, root, c.Markers[0], c.Reads[0])
		},
	},
}

// f0VerticalToken is the lowercase token a vertical's own source paths and
// constants carry. Derived from the Vertical constant itself so a renamed
// vertical cannot leave a stale token behind.
func f0VerticalToken(v verticals.Vertical) string { return strings.ToLower(string(v)) }

// f0ForeignVerticalTokens returns the tokens of every vertical EXCEPT own.
//
// Derived from verticals.AllVerticals — the production list — rather than from
// whatever map the rule is being fed, so adding a third vertical extends the
// foreign-token rule automatically instead of leaving a blind spot that only
// shows up the next time someone tries the attack.
func f0ForeignVerticalTokens(own verticals.Vertical) []string {
	out := make([]string, 0, len(verticals.AllVerticals))
	for _, v := range verticals.AllVerticals {
		if v != own {
			out = append(out, f0VerticalToken(v))
		}
	}
	return out
}

// f0CellPathBlind lists the Held/Gap cells whose declaration legitimately does
// NOT carry their vertical's token, each with a mandatory reason.
//
// Same shape as f0Undetectable and locked the same way (a hard count in
// TestADR0025_F0_DetectorBindingIsVerticalSpecific), and for the same reason: an
// exception list nobody counts is not an exception list, it is the soft hole
// that replaces the one you just closed. An entry that is no longer needed —
// because the cell's paths or discriminator DID come to carry the token — is a
// failure too, so the list cannot quietly accumulate dead weight.
var f0CellPathBlind = map[verticals.Key]string{
	{Invariant: verticals.InvariantLoopDeclaresTypes, Vertical: verticals.VerticalMovie}: "" +
		"The carrier is the regrab loop (cmd/server/loops/regrab.go), named by the ADR as " +
		"the MOVIE side of this invariant because regrab is what declares supported " +
		"arr_instance types; nothing in its path or in the log marker says \"movie\". There " +
		"is also no twin cell to be confused with: loop_declares_types/series is declared " +
		"undetectable (torrentsync declares nothing at all), so a mis-binding could not " +
		"borrow another cell's evidence even in principle.",
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalSeries}: "" +
		"Status.Subject on this cell is \"enrichment bounded context\": the carrier is the " +
		"enrichment bc, not the series entity, so its files are enrichment_refresh_metrics.go " +
		"/ refresh_scheduler.go / wiring. Its twin reads a disjoint set (the search files), " +
		"which is what keeps the pair distinguishable.",
	{Invariant: verticals.InvariantMetricsNamespace, Vertical: verticals.VerticalMovie}: "" +
		"Status.Subject on this cell is \"search bounded context (ADR-0024, internal/search)\". " +
		"The movie VERTICAL always exported metrics; the bounded context that shipped with " +
		"none was search, which is what the R4 table records here. Probing for " +
		"seasonfill_movie_ would be the lie Subject exists to prevent.",
}

// f0BindingViolations applies the B-46 binding rule to an arbitrary detector map
// and returns one line per violation. Empty means clean.
//
// It takes its inputs as parameters rather than reading the package vars so the
// adversarial suite can feed it a deliberately mis-bound map and prove the rule
// bites — a rule only ever run against a passing input is indistinguishable from
// a rule that always returns nothing, which is the failure this whole file is
// about.
//
// The five things it asserts:
//
//  1. A cell declares what it reads: a non-empty Reads of clean repo-relative
//     slash paths, and a non-nil Probe.
//  2. A cell is identifiable as its own vertical's: the vertical token appears
//     in a read path or in the discriminator — or the cell is listed in
//     pathBlind with a reason.
//     2-bis. A cell never reads ANOTHER vertical's file while reading none of its
//     own — regardless of its Discriminator, and excusable only through
//     pathBlind. Rule 2 alone accepted that shape: put both failure_journal
//     cells on series_worker.go, give the movie one Discriminator "movie", and
//     the token was "present" so the cell looked bound. Combined with a body
//     that consumed the discriminator vacuously (`_ = c.Discriminator`), review
//     #3 got the registry to report failure_journal/movie as Held with
//     movie_worker.go gutted. A discriminator can tell two cells on a SHARED,
//     vertical-neutral file apart — the retry_sweep shape, one wiring file — but
//     it cannot make a cell that only ever reads the OTHER vertical's source
//     into a statement about this one. Which is why this clause does not consult
//     it.
//  3. pathBlind carries no stale entries: an exception for a cell that DOES
//     carry its token is itself a violation.
//  4. Two cells of one invariant are never interchangeable: same reads AND same
//     discriminator is a violation, and when the reads are identical both
//     discriminators must be non-empty — otherwise the only thing telling the
//     two cells apart would again be prose inside a closure.
func f0BindingViolations(cells map[verticals.Key]f0Cell, pathBlind map[verticals.Key]string) []string {
	var out []string
	carries := map[verticals.Key]bool{}

	for key, cell := range cells {
		if cell.Probe == nil {
			out = append(out, key.String()+": no probe")
		}
		if len(cell.Reads) == 0 {
			out = append(out, key.String()+": declares no Reads — the probe could open any "+
				"file and the binding rule would have nothing to check")
		}
		for _, rel := range cell.Reads {
			if rel == "" || strings.Contains(rel, `\`) || strings.HasPrefix(rel, "/") ||
				strings.Contains(rel, "..") {
				out = append(out, key.String()+": Reads entry "+strconv.Quote(rel)+
					" is not a clean repo-relative slash path")
			}
		}

		token := f0VerticalToken(key.Vertical)
		has := strings.Contains(strings.ToLower(cell.Discriminator), token)
		for _, rel := range cell.Reads {
			if strings.Contains(strings.ToLower(rel), token) {
				has = true
			}
		}
		carries[key] = has

		reason, excused := pathBlind[key]
		switch {
		case has && excused:
			out = append(out, key.String()+": listed in f0CellPathBlind, but its declaration "+
				"DOES carry the vertical token "+strconv.Quote(token)+" — delete the stale "+
				"exception instead of leaving the list looking bigger than it is")
		case !has && !excused:
			out = append(out, key.String()+": neither its Reads "+strings.Join(cell.Reads, ", ")+
				" nor its discriminator "+strconv.Quote(cell.Discriminator)+" mentions "+
				strconv.Quote(token)+" — the probe is not bound to this vertical's code and "+
				"would report Held on the strength of another vertical's. Point it at this "+
				"vertical, give it a discriminator, or add an f0CellPathBlind entry saying "+
				"why neither is possible")
		case !has && excused && strings.TrimSpace(reason) == "":
			out = append(out, key.String()+": f0CellPathBlind entry with an empty reason")
		}

		// Rule 2-bis. Reading ANOTHER vertical's file while reading none of
		// your own is a mis-binding whatever the Discriminator says, and this
		// clause deliberately does not look at the Discriminator at all.
		ownInPath, foreign := false, ""
		for _, rel := range cell.Reads {
			lower := strings.ToLower(rel)
			if strings.Contains(lower, token) {
				ownInPath = true
			}
			for _, other := range f0ForeignVerticalTokens(key.Vertical) {
				if strings.Contains(lower, other) {
					foreign = other
				}
			}
		}
		if foreign != "" && !ownInPath && !excused {
			out = append(out, key.String()+": its Reads "+strings.Join(cell.Reads, ", ")+
				" name the "+strconv.Quote(foreign)+" vertical and never "+
				strconv.Quote(token)+". A discriminator cannot repair that: the probe would "+
				"be answering a question about another vertical's source and reporting the "+
				"answer under this one's name (B-46 review #3, attack A2 — both journal "+
				"cells on series_worker.go, told apart by a discriminator the bodies only "+
				"assigned to _). Point the cell at this vertical's file, or add an "+
				"f0CellPathBlind entry saying why its carrier is not this vertical's code")
		}
	}

	byInvariant := map[verticals.Invariant][]verticals.Key{}
	for key := range cells {
		byInvariant[key.Invariant] = append(byInvariant[key.Invariant], key)
	}
	for inv, keys := range byInvariant {
		sort.Slice(keys, func(i, j int) bool { return keys[i].Vertical < keys[j].Vertical })
		for i := range keys {
			for j := i + 1; j < len(keys); j++ {
				a, b := cells[keys[i]], cells[keys[j]]
				if f0ReadKey(a.Reads) != f0ReadKey(b.Reads) {
					continue
				}
				if a.Discriminator == b.Discriminator {
					out = append(out, string(inv)+": "+keys[i].String()+" and "+
						keys[j].String()+" declare the same reads AND the same "+
						"discriminator — one of them asserts nothing, and whichever "+
						"vertical is actually broken stays green on the other's evidence")
					continue
				}
				if a.Discriminator == "" || b.Discriminator == "" {
					out = append(out, string(inv)+": "+keys[i].String()+" and "+
						keys[j].String()+" read the same files, so each needs a non-empty "+
						"Discriminator — otherwise nothing outside a closure comment "+
						"distinguishes them")
				}
			}
		}
	}

	sort.Strings(out)
	return out
}

// f0ReadKey canonicalises a read set for comparison: order must not matter.
func f0ReadKey(reads []string) string {
	cp := append([]string(nil), reads...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

// ---------------------------------------------------------------------
// the probe contract: what a closure may look at
// ---------------------------------------------------------------------

// f0SelfSourcePath is the absolute path of THIS source file.
//
// The suite reading its own source is what turns the f0Cell docblock from a
// promise into a check: the contract is about what the TEXT of a probe body may
// contain, which no amount of runtime structure can observe.
func f0SelfSourcePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller(0) failed")
	abs, err := filepath.Abs(file)
	require.NoError(t, err)
	return abs
}

// f0PackageValueNames returns every package-level const and var name declared in
// the .go files of dir.
//
// This is the banned set for probe bodies, and it is DERIVED rather than
// spelled: a rule that banned the prefix "f0Rel" would be evaded by renaming the
// constant, which is not a defence at all. Anything the package holds as a value
// — path constants, metric family lists, call-site lists, the detector map
// itself — is out of reach of a probe body, whatever it is called.
func f0PackageValueNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoErrorf(t, err, "read %s", dir)
	out := map[string]bool{}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		require.NoErrorf(t, perr, "parse %s", e.Name())
		seen++
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, nm := range vs.Names {
					if nm.Name != "_" {
						out[nm.Name] = true
					}
				}
			}
		}
	}
	require.NotZerof(t, seen, "no .go files parsed under %s", dir)
	require.NotEmptyf(t, out, "no package-level values found under %s — the banned set "+
		"would be empty and the probe contract would accept anything", dir)
	return out
}

// f0Render prints an AST node back as source, for violation messages.
func f0Render(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return "<unprintable>"
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// f0MapLiteral finds `var name = map[...]...{...}` in a parsed file.
func f0MapLiteral(f *ast.File, name string) *ast.CompositeLit {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, nm := range vs.Names {
				if nm.Name != name || i >= len(vs.Values) {
					continue
				}
				if lit, ok := vs.Values[i].(*ast.CompositeLit); ok {
					return lit
				}
			}
		}
	}
	return nil
}

// f0ProbeContractViolations is the H-1/H-2 lock: it parses src, finds the
// f0Cell map literal assigned to mapName, and holds every Probe body to the
// contract described on f0Cell. Empty means clean.
//
// Why source text and not the runtime map. The two escapes the second B-46
// review demonstrated are both INVISIBLE at runtime: a body that calls
// f0DetectFailureJournal(t, root, f0RelSeriesWorker) has exactly the same type,
// the same signature and the same behaviour-on-paper as one that passes
// c.Reads[0] — it simply looks somewhere else. And a Discriminator that no body
// mentions is a perfectly normal string field. Only the TEXT of the closure
// distinguishes them, so the rule reads the text.
//
// src and banned are parameters, like f0BindingViolations' map, so
// TestADR0025_F0_DetectorBindingRuleBites can feed the rule deliberately broken
// probe bodies. A rule only ever run against the real, passing source cannot be
// told apart from a rule that always returns nothing — which is the entire
// subject of this file.
//
// The five things it asserts about each cell:
//
//  1. Probe is a function LITERAL written in the cell, not a reference to some
//     shared function whose body lives out of sight of this rule.
//  2. The closure names its cell parameter — `_ f0Cell` is the shape both
//     metrics probes had, and a probe that discards its cell cannot be bound to
//     it by anything.
//  3. The body contains no string literal, no package-level const or var, and
//     no qualified identifier from another package. Those are the three ways a
//     path or a marker can enter a body from anywhere but the cell.
//  4. The body consumes c.Reads: a declared read set nothing opens is prose.
//  5. Discriminator and Markers are consumed if and only if they are declared.
//     Declared-but-unconsumed is the H-2 vector — f0BindingViolations is
//     satisfied by the mere PRESENCE of two different strings, so two cells on
//     one foreign file passed while their discriminators did nothing.
//  6. Consumption is not the blank identifier. `_ = c.Discriminator` satisfied
//     rule 5 to the letter and consumed nothing; review #3 used it to walk H-2
//     back in. See f0BlankConsumption.
//
// What it does NOT assert, and cannot, is WHICH of the declared reads a body
// opens — the body is text, and c.Reads[0] and c.Reads[1] are the same text
// shape. That is the behavioural half, f0ProbeOpenViolations.
func f0ProbeContractViolations(t *testing.T, src, mapName string, banned map[string]bool) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "detectors.go", src, 0)
	require.NoErrorf(t, err, "parse the detector source")

	lit := f0MapLiteral(f, mapName)
	require.NotNilf(t, lit, "no `var %s = map[...]f0Cell{...}` found — the probe contract "+
		"would silently check nothing", mapName)
	require.NotEmptyf(t, lit.Elts, "%s is empty", mapName)

	var out []string
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			out = append(out, f0Render(fset, el)+": not a key/value entry")
			continue
		}
		out = append(out, f0CellLiteralViolations(fset, f0Render(fset, kv.Key), kv.Value, banned)...)
	}
	sort.Strings(out)
	return out
}

// f0CellLiteralViolations applies the contract to ONE cell literal.
func f0CellLiteralViolations(
	fset *token.FileSet, label string, value ast.Expr, banned map[string]bool,
) []string {
	cellLit, ok := value.(*ast.CompositeLit)
	if !ok {
		return []string{label + ": the cell is not written as a literal, so its probe cannot " +
			"be read by this rule"}
	}

	fields := map[string]ast.Expr{}
	for _, e := range cellLit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			fields[id.Name] = kv.Value
		}
	}

	probe, declared := fields["Probe"]
	if !declared {
		return []string{label + ": declares no Probe"}
	}
	fn, isLit := probe.(*ast.FuncLit)
	if !isLit {
		return []string{label + ": Probe is " + f0Render(fset, probe) + ", not a function " +
			"literal — the body must be written in the cell where this rule (and a reviewer) " +
			"can see which file it opens"}
	}

	var out []string
	params := map[string]bool{}
	cellParam := ""
	for _, p := range fn.Type.Params.List {
		isCell := f0Render(fset, p.Type) == "f0Cell"
		for _, nm := range p.Names {
			params[nm.Name] = true
			if isCell && nm.Name != "_" {
				cellParam = nm.Name
			}
		}
	}
	if cellParam == "" {
		out = append(out, label+": the probe does not name its f0Cell parameter, so it cannot "+
			"take its paths or its markers from the cell — every path it uses must be coming "+
			"from somewhere this declaration does not mention")
	}

	used := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			out = append(out, f0BlankConsumption(label, cellParam, x)...)
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				out = append(out, label+": the probe body contains the string literal "+
					x.Value+". A path or a marker written inside the closure is invisible to "+
					"the binding rule — declare it in Reads, Discriminator or Markers and read "+
					"it off the cell")
			}
		case *ast.SelectorExpr:
			base, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case cellParam != "" && base.Name == cellParam:
				used[x.Sel.Name] = true
			case params[base.Name]:
			default:
				out = append(out, label+": the probe body reaches outside its cell via "+
					f0Render(fset, x)+" — a constant from another package is a path or a "+
					"marker in disguise; route it through the cell or through an f0 helper")
			}
			return false
		case *ast.Ident:
			if banned[x.Name] {
				out = append(out, label+": the probe body names the package-level value "+
					x.Name+". Paths and markers must come from the cell it was handed: this "+
					"is exactly the rebinding the B-46 review reproduced (honest Reads, body "+
					"pointed at another vertical's file, suite green)")
			}
		}
		return true
	})

	if cellParam != "" && !used["Reads"] {
		out = append(out, label+": the probe never consumes "+cellParam+".Reads — the declared "+
			"read set is then prose, and the files the probe actually opens are unchecked")
	}
	out = append(out, f0FieldConsumption(label, "Discriminator", fields, used, cellParam)...)
	out = append(out, f0FieldConsumption(label, "Markers", fields, used, cellParam)...)
	return out
}

// f0BlankConsumption catches the cheapest way to satisfy the consumption rule
// without consuming anything: `_ = c.Discriminator`.
//
// Review #3 used exactly that, paired with both failure_journal cells pointed at
// series_worker.go, to get a gutted movie worker reported as Held. The
// consumption rule is syntactic by nature, so it cannot tell a real use from a
// fake one in general — but assignment to the blank identifier is not a use in
// ANY reading of the word, it is a statement whose only effect is to appease a
// checker. That specific form is worth banning outright.
func f0BlankConsumption(label, cellParam string, as *ast.AssignStmt) []string {
	if cellParam == "" {
		return nil
	}
	var out []string
	for i, lhs := range as.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name != "_" || i >= len(as.Rhs) {
			continue
		}
		field := f0CellFieldRef(cellParam, as.Rhs[i])
		if field == "" {
			continue
		}
		out = append(out, label+": the probe assigns "+cellParam+"."+field+" to the blank "+
			"identifier. That is not consumption, it is a statement written to satisfy this "+
			"rule — declare the field only if the probe's ANSWER depends on it (B-46 review "+
			"#3, attack A2)")
	}
	return out
}

// f0CellFieldRef returns the name of the first f0Cell field referenced anywhere
// inside expr, or "" if the expression does not touch the cell.
func f0CellFieldRef(cellParam string, expr ast.Expr) string {
	name := ""
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if base, isIdent := sel.X.(*ast.Ident); isIdent && base.Name == cellParam {
			name = sel.Sel.Name
			return false
		}
		return true
	})
	return name
}

// f0FieldConsumption pairs a declared cell field with its use in the body: both
// or neither.
func f0FieldConsumption(
	label, field string, fields map[string]ast.Expr, used map[string]bool, cellParam string,
) []string {
	_, declared := fields[field]
	consumed := used[field]
	switch {
	case declared && !consumed:
		return []string{label + ": declares " + field + " but the probe never consumes " +
			cellParam + "." + field + ". An unconsumed token is decoration — the binding rule " +
			"is satisfied by its mere presence, which is how two cells reading ONE foreign " +
			"file passed review with discriminators that did nothing"}
	case !declared && consumed:
		return []string{label + ": the probe consumes " + cellParam + "." + field +
			", which this cell does not declare — it would read the zero value and assert " +
			"nothing"}
	}
	return nil
}

// f0AnyFieldContains reports whether any of fields carries token as a substring.
//
// A named helper rather than strings.Contains written inline, because a probe
// body may not reach into another package (the contract on f0Cell) — and the
// deferred guard below is now held to that contract like every other probe.
func f0AnyFieldContains(fields []string, token string) bool {
	for _, f := range fields {
		if strings.Contains(f, token) {
			return true
		}
	}
	return false
}

// f0DeferredGuards keep a Deferred cell from rotting: the guard asserts
// that the PREMISE of the deferral still holds. A true result means "the
// reason to defer is still true".
//
// These are f0Cells, not a separate probe type, and that is the point (B-46
// review #3, MED nit). The previous shape was `map[Key]func(t, root) bool`: it
// carried no read set, so the one path it needed — f0RelGrabRecord — had to be
// spelled INSIDE the body. That was the only place left in the file where a
// probe named its own file, i.e. exactly the H-1 shape the contract exists to
// forbid, living in an unlocked corner because the type could not express a
// declaration. Rather than document the asymmetry, it is gone: deferred guards
// declare their reads, take them from the cell, and are run through
// f0BindingViolations, f0ProbeContractViolations and the opened-set rule
// alongside f0Detectors (see TestADR0025_F0_DetectorBindingIsVerticalSpecific).
//
// The ONE thing that stays asymmetric, stated rather than hidden: the polarity
// of the answer. For an f0Detectors cell, true means "the code carries the
// invariant"; for a guard, true means "the reason to DEFER still holds" — the
// invariant is still absent. Same contract, opposite reading, which is why they
// live in two maps and why TestADR0025_F0_RegistryMatchesCode refuses to let one
// cell appear in both.
var f0DeferredGuards = map[verticals.Key]f0Cell{
	{Invariant: verticals.InvariantRegrabSupported, Vertical: verticals.VerticalMovie}: {
		// Premise: there is no movie-grab infrastructure at all —
		// grab.Record carries no movie field (grab.go:58,60 key by
		// SeriesID + SeasonNumber). If that ever changes, the deferral
		// must be revisited rather than silently inherited.
		//
		// B-46 audit: this substring match is BROAD on purpose and, unlike every
		// other Contains B-46 removed, broad here means STRICTER. A
		// deferred-premise guard asserts "the reason to defer still holds"; the
		// widest possible match makes the premise EASIEST to falsify, so any
		// movie-shaped field appearing on grab.Record — MovieID,
		// ExternalMovieID, RadarrMovieID — trips it. The failure mode a narrow
		// match would create (premise silently inherited after movie-grab
		// infrastructure lands) is exactly what B-41 must not be allowed to do
		// quietly. Reviewed and kept.
		Reads:         []string{f0RelGrabRecord},
		Discriminator: "Movie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return !f0AnyFieldContains(f0GrabRecordFields(t, root, c.Reads[0]), c.Discriminator)
		},
	},
}

// f0Undetectable lists cells that cannot be honestly probed at F0. An
// entry here is a declaration in its own right: the reason is mandatory
// and the suite fails if a cell is neither probed nor listed.
var f0Undetectable = map[verticals.Key]string{
	{Invariant: verticals.InvariantLoopDeclaresTypes, Vertical: verticals.VerticalSeries}: "" +
		"The ADR grounds this cell in live production observation " +
		"(torrentsync_reconciler_start/_done with instance=radarr every ~30s, no errors), " +
		"not in a property of the source tree. F2 made the MOVIE side of this invariant " +
		"detectable by giving regrab an explicit supported-type declaration; torrentsync " +
		"still declares nothing, because it genuinely supports every type — and in a source " +
		"scan \"declares nothing\" is indistinguishable from \"forgot to declare\". Any static " +
		"probe here would be an imitation of a check, so this cell stays declared " +
		"undetectable rather than getting a fake one.",
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
			cell, hasProbe := f0Detectors[entry.Key]
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
				stillDeferred, opened := f0RunProbe(t, root, guard)
				require.Empty(t,
					f0ProbeOpenViolations(entry.Key.String()+" (deferred guard)",
						guard.Reads, opened),
					"the guard's declared reads and the files it opened are not the same set")
				require.Truef(t, stillDeferred,
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
			gotHeld, opened := f0RunProbe(t, root, cell)
			// Checked BEFORE the Held/Gap comparison on purpose: if the probe
			// did not read what the cell declared, its answer is about some
			// other file and the comparison below is meaningless whichever way
			// it comes out.
			require.Empty(t, f0ProbeOpenViolations(entry.Key.String(), cell.Reads, opened),
				"the cell's declared Reads and the files its probe opened are not the same "+
					"set — the declaration does not describe the test that ran")
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

// TestADR0025_F0_DetectorBindingIsVerticalSpecific is B-2 of the B-46 review:
// the rule that a cell's probe must look at THAT cell's vertical, enforced by a
// test instead of by a comment.
//
// What it would have caught, reproduced by the reviewer on the previous
// revision: rebind the failure_journal/movie closure to series_worker.go and the
// movie cell reports Held with internal/enrichment/app/movie_worker.go gutted to
// an empty file. The conformance suite passed. The adversarial selftest passed —
// it tests the detector FUNCTIONS, and the function was working perfectly on the
// file it was handed. Nothing tested WHICH file it was handed. That is the same
// shape as the domain_logger bug B-46 fixed one level up, and the most likely
// place for the class to come back a fourth time.
//
// The declaration rule was not enough, and the second B-46 review proved it
// with two edits that this test USED to pass:
//
//   - H-1: leave Reads honest ([]string{f0RelMovieWorker}), change the body's
//     one argument from c.Reads[0] to f0RelSeriesWorker, gut movie_worker.go →
//     green. The binding rule reads declarations; nothing read the body.
//   - H-2: point BOTH failure_journal cells at series_worker.go and tell them
//     apart with discriminators "series"/"movie" that neither body mentions →
//     green. Rule 2 was satisfied by the token being PRESENT in a string field.
//
// Both are closed by the second half, f0ProbeContractViolations, which parses
// this very file and holds each Probe body to the contract documented on
// f0Cell: no literals, no package-level values, no foreign constants, and
// Discriminator / Markers consumed exactly when declared.
//
// Four guards, not one:
//
//   - the binding rule on the declarations (f0BindingViolations, also fed
//     deliberately mis-bound maps by TestADR0025_F0_DetectorBindingRuleBites);
//   - the probe contract on the bodies (f0ProbeContractViolations, fed
//     deliberately broken bodies by the same test);
//   - every declared read must EXIST, so a cell cannot be excused by pointing at
//     a path that was renamed away — a missing file makes several probes return
//     a clean false, which would read as an honest Gap;
//   - hard count locks on f0CellPathBlind, the one escape hatch, exactly as
//     TestADR0025_F0_UndetectableSetIsLocked locks the other one.
func TestADR0025_F0_DetectorBindingIsVerticalSpecific(t *testing.T) {
	t.Parallel()
	root := f0RepoRoot(t)

	require.Empty(t, f0BindingViolations(f0Detectors, f0CellPathBlind),
		"detector cells are not bound to their own vertical")
	require.Empty(t, f0BindingViolations(f0DeferredGuards, nil),
		"a deferred-premise guard is not bound to its own vertical. Guards are f0Cells and "+
			"are held to the same rules; there is no pathBlind list for them because there "+
			"is no guard today that needs one")

	self := f0SelfSourcePath(t)
	src, err := os.ReadFile(self)
	require.NoErrorf(t, err, "read %s", self)
	banned := f0PackageValueNames(t, filepath.Dir(self))
	require.Empty(t,
		f0ProbeContractViolations(t, string(src), "f0Detectors", banned),
		"a detector probe takes a path or a marker from somewhere other than its cell, or "+
			"declares one it never consumes — see the contract on f0Cell")
	require.Empty(t,
		f0ProbeContractViolations(t, string(src), "f0DeferredGuards", banned),
		"a deferred-premise guard takes a path or a token from somewhere other than its "+
			"cell. This used to be the one exempt corner of the file: guards had no read set, "+
			"so their single path was spelled in the body — the H-1 shape, unlocked")

	for _, m := range []map[verticals.Key]f0Cell{f0Detectors, f0DeferredGuards} {
		for key, cell := range m {
			for _, rel := range cell.Reads {
				_, err := os.Stat(f0Path(t, root, rel))
				require.NoErrorf(t, err,
					"%s declares it reads %s, which does not exist. Several probes treat a "+
						"missing file as a clean false, so a stale declaration would read as "+
						"an honest Gap; fix the path or the cell.", key, rel)
			}
		}
	}

	require.Lenf(t, f0CellPathBlind, 3,
		"exactly three cells may be excused from naming their own vertical today: "+
			"loop_declares_types/movie and the two metrics_namespace cells, whose carrier is a "+
			"bounded context (Status.Subject), not the vertical. Growing this set turns off the "+
			"binding rule for another cell — if that is genuinely intended, say why in the entry "+
			"and bump this count in the same change (ADR-0025 R2-bis)")

	for key, reason := range f0CellPathBlind {
		require.Containsf(t, f0Detectors, key,
			"%s is excused from the binding rule but is not a detector cell at all — a dead "+
				"exception entry", key)
		require.NotEmptyf(t, reason, "%s: excused from the binding rule without a reason", key)
	}

	// The two metrics_namespace exceptions rest on Status.Subject naming a
	// carrier other than the vertical. Cross-check it against the production
	// declaration rather than trusting the prose in the exception: if someone
	// drops Subject, the excuse evaporates and this fails.
	for _, entry := range verticals.All() {
		if entry.Key.Invariant != verticals.InvariantMetricsNamespace {
			continue
		}
		if _, excused := f0CellPathBlind[entry.Key]; !excused {
			continue
		}
		require.NotEmptyf(t, entry.Status.Subject,
			"%s is excused from the binding rule because its carrier is a bounded context, "+
				"but the registry no longer declares a Subject for it", entry.Key)
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

		// ADR-0025 F1: seeded through the REAL repository now, exactly like the
		// series arm above. Before F1 this had to be a raw INSERT because
		// EntityTypeMovie / SourceTMDBMovie did not exist and RecordFailure
		// rejected the row at its own validator (that WAS the gap). Using the
		// typed path means this test now also proves the new enum values
		// survive persistence, instead of only proving the SQL gate.
		require.NoError(t, errRepo.RecordFailure(ctx, enrichdomain.EnrichmentError{
			EntityType:  enrichdomain.EntityTypeMovie,
			EntityID:    int64(movieID),
			Source:      enrichdomain.SourceTMDBMovie,
			LastError:   "ADR-0025 F0 parity probe",
			Attempts:    99,
			FirstSeenAt: now,
			LastSeenAt:  now,
		}))
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
