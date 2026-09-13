//go:build integration

// ADR-0025 F0 / B-46 — the detectors of adr0025_f0_vertical_parity_test.go,
// tested ADVERSARIALLY against themselves.
//
// Why this file exists. The parity suite asserts "the declaration matches the
// code". Nothing asserted that the DETECTORS mean what they look like they
// mean, and three times running they did not:
//
//   - F1: the probe read raw file text, so a `// TODO: EnrichmentError` comment
//     flipped a Gap into a false Held. Fixed by parsing the AST.
//   - F2: markers moved into string literals, matched with strings.Contains —
//     a near-miss event name still satisfied them.
//   - F3: metrics_namespace/movie was probed with a directory-wide prefix scan.
//     Proved false-Held BY EXPERIMENT: the metrics file was kept, every
//     increment call was deleted, and the cell stayed green.
//
// Each time a single instance was patched and the MECHANISM was left alone, so
// the class came back from another side. B-46 changes the mechanism and adds
// this file so the next weakening shows up here rather than in production.
//
// The shape of every case is a PAIR:
//
//	positive control  — a fixture with correct code  → detector MUST be true
//	adversarial       — code that LOOKS right but does not carry the invariant
//	                    → detector MUST be false
//
// The positive control is not ceremony. A detector that always returned false
// would pass every adversarial case in this file on its own; the pair is what
// makes the suite able to tell "strict" from "broken".
//
// Fixtures are synthetic .go files under t.TempDir(), laid out like the repo.
// They never have to compile or type-check — the detectors parse, they do not
// build — and no real file is touched.
//
// THREE axes are covered, not one, and each was added because the previous set
// was walked around in review:
//
//  1. the detector FUNCTIONS, attacked with synthetic source (everything above);
//  2. their BINDING to registry cells, attacked with synthetic detector maps
//     (TestADR0025_F0_DetectorBindingRuleBites) — added after the B-46 review
//     reproduced a fourth instance of the class: rebind the failure_journal/movie
//     closure to series_worker.go and the movie cell reports Held with
//     movie_worker.go gutted, every function here behaving perfectly on the
//     wrong file;
//  3. the probe BODIES, attacked with synthetic detector SOURCE
//     (TestADR0025_F0_ProbeContractBites) — added after the SECOND review showed
//     axis 2 could be stepped around twice while green: leave Reads honest and
//     hardcode another vertical's path constant in the body (H-1), or tell two
//     cells on one file apart with discriminators no body mentions (H-2).
//  4. what a probe ACTUALLY OPENED, attacked by running a mis-bound cell against
//     a synthetic tree (TestADR0025_F0_ProbeOpenSetBites) — added after the THIRD
//     review showed axes 2 and 3 are both satisfied by declaring two real paths
//     and opening c.Reads[1] instead of c.Reads[0] (attack D). Text rules cannot
//     tell two indices apart; only the record of which file f0Path resolved can.
//
// TestADR0025_F0_ThreeRulesAreOneSystem pins that each axis owns an escape the
// other two are blind to, so none of them can be deleted as already covered.
//
// One case is deliberately NOT reimplemented here: "move the cell into
// f0Undetectable and delete its detector". That escape is already locked by
// TestADR0025_F0_UndetectableSetIsLocked, which pins len(f0Undetectable) == 1;
// f0adv_TestUndetectableEscapeStaysLocked below states the cross-reference
// explicitly so the two guards are visibly one system.
package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/verticals"
)

// f0advFixture writes a synthetic repo tree and returns its root.
func f0advFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(src), 0o600))
	}
	return root
}

const f0advWorkerRel = "internal/enrichment/app/worker.go"

// --- failure_journal fixtures ----------------------------------------

const f0advJournalGood = `package app

type Deps struct{ EnrichmentErrors Repo }
type W struct{ deps Deps }

func (w *W) handleTMDBError() {
	w.recordEnrichmentError()
}

func (w *W) recordEnrichmentError() {
	w.deps.EnrichmentErrors.RecordFailure()
}
`

// Every marker identifier is present; the helper is simply never reached.
const f0advJournalHelperNeverCalled = `package app

type Deps struct{ EnrichmentErrors Repo }
type W struct{ deps Deps }

func (w *W) handleTMDBError() {
}

func (w *W) recordEnrichmentError() {
	w.deps.EnrichmentErrors.RecordFailure()
}
`

// RecordFailure is called — on an audit sink, not on the journal repository.
const f0advJournalWrongReceiver = `package app

type Deps struct{ EnrichmentErrors Repo }
type W struct {
	deps  Deps
	audit Audit
}

func (w *W) handleTMDBError() { w.recordEnrichmentError() }

func (w *W) recordEnrichmentError() {
	w.audit.RecordFailure()
}
`

// Near-miss RENAME of the journal dependency. This is the f0IdentUsed claim
// under attack: the pre-B-46 probe matched a SUBSTRING of any identifier, and
// "EnrichmentErrorsRepo" contains "EnrichmentErrors", so the rename was
// invisible while the field the worker actually writes through changed name.
// Note the third marker still passes on its own — f0CallSpec.Receiver is
// deliberately a Contains — so this case pins the exactness of the FIRST marker
// specifically.
const f0advJournalIdentRenamedRepo = `package app

type Deps struct{ EnrichmentErrorsRepo Repo }
type W struct{ deps Deps }

func (w *W) handleTMDBError() { w.recordEnrichmentError() }

func (w *W) recordEnrichmentError() {
	w.deps.EnrichmentErrorsRepo.RecordFailure()
}
`

// The other half of the same near-miss family: a versioned suffix.
const f0advJournalIdentRenamedV2 = `package app

type Deps struct{ EnrichmentErrorsV2 Repo }
type W struct{ deps Deps }

func (w *W) handleTMDBError() { w.recordEnrichmentError() }

func (w *W) recordEnrichmentError() {
	w.deps.EnrichmentErrorsV2.RecordFailure()
}
`

// All three markers appear as a type name and two field names. Nothing runs.
const f0advJournalIdentifiersOnly = `package app

type RecordFailure struct{}

type Deps struct {
	EnrichmentErrors      Repo
	recordEnrichmentError func()
}
`

func TestADR0025_F0_DetectorFailureJournalIsStrict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"positive control", f0advJournalGood, true},
		{"helper declared but never called", f0advJournalHelperNeverCalled, false},
		{"RecordFailure called on the wrong receiver", f0advJournalWrongReceiver, false},
		{"markers exist only as a type and two field names", f0advJournalIdentifiersOnly, false},
		{"journal dependency renamed to EnrichmentErrorsRepo", f0advJournalIdentRenamedRepo, false},
		{"journal dependency renamed to EnrichmentErrorsV2", f0advJournalIdentRenamedV2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{f0advWorkerRel: tc.src})
			require.Equal(t, tc.want, f0DetectFailureJournal(t, root, f0advWorkerRel))
		})
	}
}

// --- picker_breaker fixtures -----------------------------------------

const f0advPickerRel = "internal/enrichment/persistence/query.go"

func f0advPickerSrc(sql string) string {
	return "package persistence\n\nconst q = `" + sql + "`\n"
}

const f0advSQLAllArmsGated = `
SELECT a FROM t WHERE NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
UNION ALL
SELECT b FROM t WHERE NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
UNION ALL
SELECT c FROM t WHERE NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
`

// Three gates, three arms — the old `count >= arms` arithmetic held exactly,
// while two of three tiers re-picked terminally-dead rows every tick.
const f0advSQLGatesPileUpInOneArm = `
SELECT a FROM t WHERE NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
  AND NOT EXISTS (SELECT 1 FROM enrichment_errors ee2 WHERE ee.attempts > 5)
  AND NOT EXISTS (SELECT 1 FROM enrichment_errors ee3 WHERE ee.attempts > 5)
UNION ALL
SELECT b FROM t WHERE true
UNION ALL
SELECT c FROM t WHERE true
`

const f0advSQLGateOnlyInComment = `
SELECT a FROM t WHERE NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
UNION ALL
-- terminal gate removed here: NOT EXISTS (SELECT 1 FROM enrichment_errors ee WHERE ee.attempts > 5)
SELECT b FROM t WHERE true
`

func TestADR0025_F0_DetectorPickerBreakerIsPerArm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"positive control", f0advSQLAllArmsGated, true},
		{"all three gates stacked in the first arm", f0advSQLGatesPileUpInOneArm, false},
		{"gate survives only inside a SQL comment", f0advSQLGateOnlyInComment, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{f0advPickerRel: f0advPickerSrc(tc.sql)})
			require.Equal(t, tc.want, f0DetectPickerBreaker(t, root, f0advPickerRel))
		})
	}
}

// --- search metrics fixtures -----------------------------------------

const f0advSearchMetricsGood = `package observability

const (
	MetricSearchRequestsTotal          = "seasonfill_search_requests_total"
	MetricSearchRequestDurationSeconds = "seasonfill_search_request_duration_seconds"
	MetricSearchGroupQueriesTotal      = "seasonfill_search_group_queries_total"
	MetricSearchGroupDurationSeconds   = "seasonfill_search_group_duration_seconds"
)
`

const f0advSearchMetricsNearMiss = `package observability

const (
	MetricSearchRequestsTotal          = "seasonfill_search_requests_total_v2"
	MetricSearchRequestDurationSeconds = "seasonfill_search_request_duration_seconds"
	MetricSearchGroupQueriesTotal      = "seasonfill_search_group_queries_total"
	MetricSearchGroupDurationSeconds   = "seasonfill_search_group_duration_seconds"
)
`

const f0advSearchHandlerGood = `package rest

func h() { observability.ObserveSearchRequest("all", "ok", start) }
`

const f0advSearchHandlerNoCall = `package rest

type observeSearchRequestHook func()

var _ = "ObserveSearchRequest"
`

const f0advSearchUsecaseGood = `package app

func run() {
	observeGroup(observability.SearchEntitySeries, 1, nil, start)
	observeGroup(observability.SearchEntityMovies, 1, nil, start)
	observeGroup(observability.SearchEntityCollections, 1, nil, start)
	observeGroup(observability.SearchEntityPeople, 1, nil, start)
}

func observeGroup(entity string, hits int, err error, start time.Time) {
	observability.ObserveSearchGroup(entity, observability.SearchSourceLibrary, hits, err, start)
}
`

const f0advSearchAdapterGood = `package catalog

func (a *Adapter) run() {
	a.observeGroup(observability.SearchEntitySeries, 0, err, start)
	a.observeGroup(observability.SearchEntitySeries, 1, nil, start)
	a.observeGroup(observability.SearchEntityMovies, 0, err, start)
	a.observeGroup(observability.SearchEntityMovies, 1, nil, start)
	a.observeGroup(observability.SearchEntityCollections, 0, err, start)
	a.observeGroup(observability.SearchEntityCollections, 1, nil, start)
	a.observeGroup(observability.SearchEntityPeople, 0, err, start)
	a.observeGroup(observability.SearchEntityPeople, 1, nil, start)
}

func (a *Adapter) observeGroup(entity string, hits int, err error, start time.Time) {
	observability.ObserveSearchGroup(entity, observability.SearchSourceCatalog, hits, err, start)
}
`

// THE vector B-46 was opened for: all eight call sites deleted, the helper (and
// therefore the ObserveSearchGroup reference) left behind. Every identifier the
// pre-B-46 detector looked for is still present.
const f0advSearchAdapterHelperOnly = `package catalog

func (a *Adapter) run() {
}

func (a *Adapter) observeGroup(entity string, hits int, err error, start time.Time) {
	observability.ObserveSearchGroup(entity, observability.SearchSourceCatalog, hits, err, start)
}
`

// Partial gutting: one entity group still observed, three silently dropped.
const f0advSearchAdapterHalfGutted = `package catalog

func (a *Adapter) run() {
	a.observeGroup(observability.SearchEntitySeries, 0, err, start)
	a.observeGroup(observability.SearchEntitySeries, 1, nil, start)
}

func (a *Adapter) observeGroup(entity string, hits int, err error, start time.Time) {
	observability.ObserveSearchGroup(entity, observability.SearchSourceCatalog, hits, err, start)
}
`

func f0advSearchFixture(metrics, handler, usecase, adapter string) map[string]string {
	return map[string]string{
		"internal/observability/search_metrics.go": metrics,
		"internal/search/rest/handler.go":          handler,
		"internal/search/app/usecase.go":           usecase,
		"internal/search/catalog/adapter.go":       adapter,
	}
}

func TestADR0025_F0_DetectorSearchMetricsRequiresIncrements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{
			"positive control",
			f0advSearchFixture(f0advSearchMetricsGood, f0advSearchHandlerGood,
				f0advSearchUsecaseGood, f0advSearchAdapterGood),
			true,
		},
		{
			"all eight adapter call sites deleted, helper kept",
			f0advSearchFixture(f0advSearchMetricsGood, f0advSearchHandlerGood,
				f0advSearchUsecaseGood, f0advSearchAdapterHelperOnly),
			false,
		},
		{
			"two of eight adapter call sites survive",
			f0advSearchFixture(f0advSearchMetricsGood, f0advSearchHandlerGood,
				f0advSearchUsecaseGood, f0advSearchAdapterHalfGutted),
			false,
		},
		{
			"family name is a near miss",
			f0advSearchFixture(f0advSearchMetricsNearMiss, f0advSearchHandlerGood,
				f0advSearchUsecaseGood, f0advSearchAdapterGood),
			false,
		},
		{
			"handler names the helper but never calls it",
			f0advSearchFixture(f0advSearchMetricsGood, f0advSearchHandlerNoCall,
				f0advSearchUsecaseGood, f0advSearchAdapterGood),
			false,
		},
		{
			"metrics file removed entirely reads as a clean Gap",
			map[string]string{
				"internal/search/rest/handler.go":    f0advSearchHandlerGood,
				"internal/search/app/usecase.go":     f0advSearchUsecaseGood,
				"internal/search/catalog/adapter.go": f0advSearchAdapterGood,
			},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, f0DetectSearchMetrics(t, f0advFixture(t, tc.files), f0SearchMetricReads()))
		})
	}
}

// --- enrichment metrics fixtures -------------------------------------

const f0advEnrichMetricsGood = "package observability\n\n" +
	"func (m *M) IncRefresh(tier T, result string) {\n" +
	"\tmetrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_total{tier=\"` + tier.String() + `\",result=\"` + result + `\"}`).Inc()\n" +
	"}\n\n" +
	"func (m *M) ObserveBatchSize(n int) {\n" +
	"\tmetrics.GetOrCreateGauge(`seasonfill_enrichment_refresh_batch_size`, nil).Set(float64(n))\n" +
	"}\n\n" +
	"func (m *M) ObserveTickDuration(d time.Duration) {\n" +
	"\tmetrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds`).Update(d.Seconds())\n" +
	"}\n"

// Families declared as constants, no VictoriaMetrics series ever created. The
// exact shape ADR-0025 F3 proved false-Held for the search families, applied to
// the enrichment families that f0ObservabilityHasPrefix still guarded until B-46.
const f0advEnrichMetricsConstsOnly = "package observability\n\n" +
	"const (\n" +
	"\tMetricRefreshTotal = `seasonfill_enrichment_refresh_total{tier=\"x\"}`\n" +
	"\tMetricBatchSize    = `seasonfill_enrichment_refresh_batch_size`\n" +
	"\tMetricTick         = `seasonfill_enrichment_refresh_tick_seconds`\n" +
	")\n"

// The B-46-review vector, in fixture form. metrics_namespace/series used to run
// all THREE enrichment families through f0LiteralHasPrefix under a call-site
// note claiming they were all built by concatenation. Only the counter is; the
// gauge and the histogram are whole literals, so a _v2 rename slid straight
// past — reproduced on the live adapter before the fix. These two cases are the
// enrichment twin of "family name is a near miss" in the search suite above,
// which is where the class was closed the first time.
const f0advEnrichMetricsBatchSizeV2 = "package observability\n\n" +
	"func (m *M) IncRefresh(tier T, result string) {\n" +
	"\tmetrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_total{tier=\"` + tier.String() + `\",result=\"` + result + `\"}`).Inc()\n" +
	"}\n\n" +
	"func (m *M) ObserveBatchSize(n int) {\n" +
	"\tmetrics.GetOrCreateGauge(`seasonfill_enrichment_refresh_batch_size_v2`, nil).Set(float64(n))\n" +
	"}\n\n" +
	"func (m *M) ObserveTickDuration(d time.Duration) {\n" +
	"\tmetrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds`).Update(d.Seconds())\n" +
	"}\n"

const f0advEnrichMetricsTickSecondsV2 = "package observability\n\n" +
	"func (m *M) IncRefresh(tier T, result string) {\n" +
	"\tmetrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_total{tier=\"` + tier.String() + `\",result=\"` + result + `\"}`).Inc()\n" +
	"}\n\n" +
	"func (m *M) ObserveBatchSize(n int) {\n" +
	"\tmetrics.GetOrCreateGauge(`seasonfill_enrichment_refresh_batch_size`, nil).Set(float64(n))\n" +
	"}\n\n" +
	"func (m *M) ObserveTickDuration(d time.Duration) {\n" +
	"\tmetrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds_v2`).Update(d.Seconds())\n" +
	"}\n"

// The family survives ONLY as the opening words of a prose literal while the
// real gauge was renamed. Prefix matching accepted this; f0LiteralExact does not.
const f0advEnrichMetricsFamilyOnlyInProse = "package observability\n\n" +
	"func (m *M) IncRefresh(tier T, result string) {\n" +
	"\tmetrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_total{tier=\"` + tier.String() + `\",result=\"` + result + `\"}`).Inc()\n" +
	"}\n\n" +
	"func (m *M) ObserveBatchSize(n int) {\n" +
	"\tlog.Print(`seasonfill_enrichment_refresh_batch_size was retired, see the v2 family`)\n" +
	"\tmetrics.GetOrCreateGauge(`sf_batch_size_v2`, nil).Set(float64(n))\n" +
	"}\n\n" +
	"func (m *M) ObserveTickDuration(d time.Duration) {\n" +
	"\tmetrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds`).Update(d.Seconds())\n" +
	"}\n"

// The one family that genuinely IS concatenated, renamed. The prefix moves, so
// even the deliberately-weaker f0LiteralHasPrefix rejects it — which is the
// argument for prefix matching being honest in that one place.
const f0advEnrichMetricsTotalV2 = "package observability\n\n" +
	"func (m *M) IncRefresh(tier T, result string) {\n" +
	"\tmetrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_total_v2{tier=\"` + tier.String() + `\",result=\"` + result + `\"}`).Inc()\n" +
	"}\n\n" +
	"func (m *M) ObserveBatchSize(n int) {\n" +
	"\tmetrics.GetOrCreateGauge(`seasonfill_enrichment_refresh_batch_size`, nil).Set(float64(n))\n" +
	"}\n\n" +
	"func (m *M) ObserveTickDuration(d time.Duration) {\n" +
	"\tmetrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds`).Update(d.Seconds())\n" +
	"}\n"

const f0advSchedulerGood = `package app

type RefreshMetrics interface {
	IncRefresh(tier T, result string)
	ObserveBatchSize(n int)
	ObserveTickDuration(d time.Duration)
}

func (s *S) tick() {
	defer s.deps.Metrics.ObserveTickDuration(s.deps.Clock().Sub(start))
	s.deps.Metrics.ObserveBatchSize(len(candidates))
	s.deps.Metrics.IncRefresh(c.Tier, "ok")
	s.deps.Metrics.IncRefresh(c.Tier, "skipped")
	s.deps.Metrics.IncRefresh(c.Tier, "error")
}
`

// The port still DECLARES every method, so every identifier is present, but the
// hot path increments nothing.
const f0advSchedulerInterfaceOnly = `package app

type RefreshMetrics interface {
	IncRefresh(tier T, result string)
	ObserveBatchSize(n int)
	ObserveTickDuration(d time.Duration)
}

func (s *S) tick() {
}
`

// Only the "ok" arm increments: the result label exists but never varies.
const f0advSchedulerOneResultArm = `package app

func (s *S) tick() {
	defer s.deps.Metrics.ObserveTickDuration(s.deps.Clock().Sub(start))
	s.deps.Metrics.ObserveBatchSize(len(candidates))
	s.deps.Metrics.IncRefresh(c.Tier, "ok")
}
`

const f0advWiringGood = `package wiring

func build() {
	deps.Metrics = observability.NewEnrichmentRefreshMetrics()
	_, _ = d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBSeries, now, 100)
	_, _ = d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBMovie, now, 100)
}
`

// The real adapter is never constructed, so the scheduler keeps its silent
// no-op default and /metrics exports nothing — with every literal and every
// call site above still in place.
const f0advWiringNoopMetrics = `package wiring

func build() {
	_, _ = d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBSeries, now, 100)
}
`

func f0advEnrichFixture(metrics, scheduler, wiring string) map[string]string {
	return map[string]string{
		"internal/observability/enrichment_refresh_metrics.go": metrics,
		"internal/enrichment/app/refresh_scheduler.go":         scheduler,
		"internal/wiring/enrichment.go":                        wiring,
	}
}

func TestADR0025_F0_DetectorEnrichmentMetricsRequiresIncrements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"positive control", f0advEnrichFixture(f0advEnrichMetricsGood, f0advSchedulerGood, f0advWiringGood), true},
		{"families are constants nobody increments", f0advEnrichFixture(f0advEnrichMetricsConstsOnly, f0advSchedulerGood, f0advWiringGood), false},
		{"scheduler declares the port but increments nothing", f0advEnrichFixture(f0advEnrichMetricsGood, f0advSchedulerInterfaceOnly, f0advWiringGood), false},
		{"only the ok result arm increments", f0advEnrichFixture(f0advEnrichMetricsGood, f0advSchedulerOneResultArm, f0advWiringGood), false},
		{"real adapter never wired, no-op default stands", f0advEnrichFixture(f0advEnrichMetricsGood, f0advSchedulerGood, f0advWiringNoopMetrics), false},
		{"batch_size family renamed to _v2", f0advEnrichFixture(f0advEnrichMetricsBatchSizeV2, f0advSchedulerGood, f0advWiringGood), false},
		{"tick_seconds family renamed to _v2", f0advEnrichFixture(f0advEnrichMetricsTickSecondsV2, f0advSchedulerGood, f0advWiringGood), false},
		{"batch_size survives only as the start of a prose literal", f0advEnrichFixture(f0advEnrichMetricsFamilyOnlyInProse, f0advSchedulerGood, f0advWiringGood), false},
		{"the concatenated counter family renamed to _v2", f0advEnrichFixture(f0advEnrichMetricsTotalV2, f0advSchedulerGood, f0advWiringGood), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, f0DetectEnrichmentMetrics(t, f0advFixture(t, tc.files), f0EnrichmentMetricReads()))
		})
	}
}

// --- literal exactness ------------------------------------------------

func TestADR0025_F0_DetectorLiteralMarkersAreExact(t *testing.T) {
	t.Parallel()
	const rel = "cmd/server/loops/regrab.go"
	cases := []struct {
		name string
		lit  string
		want bool
	}{
		{"positive control", "regrab_skipped_unsupported_type", true},
		{"plural near miss", "regrab_skipped_unsupported_types", false},
		{"prefixed near miss", "watchdog_regrab_skipped_unsupported_type", false},
		{"name only inside prose", "see regrab_skipped_unsupported_type for details", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "package loops\n\nfunc f() { l.logger.InfoContext(ctx, \"" + tc.lit + "\") }\n"
			root := f0advFixture(t, map[string]string{rel: src})
			got := f0LiteralExact(t,
				filepath.Join(root, "cmd", "server", "loops", "regrab.go"),
				"regrab_skipped_unsupported_type")
			require.Equal(t, tc.want, got)
		})
	}
}

// --- retry sweep ------------------------------------------------------

func TestADR0025_F0_DetectorRetrySweepMatchesExactSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"positive control", f0advWiringGood, true},
		{
			"a different Source constant that merely contains Movie",
			`package wiring

func build() {
	_, _ = d.EnrichmentErrors.ListDueForRetry(ctx, enrichment.SourceTMDBMovieLegacy, now, 100)
}
`,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{"internal/wiring/enrichment.go": tc.src})
			require.Equal(t, tc.want,
				f0RetrySweepHasSource(t, root, f0RelWiringEnrichment, "SourceTMDBMovie"))
		})
	}
}

// --- domain logger ----------------------------------------------------

func TestADR0025_F0_DetectorDomainLoggerReadsTheVertical(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		domain string
		src    string
		want   bool
	}{
		{
			"positive control", "enrichment",
			`package app

func New(deps Deps) {
	deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
}
`,
			true,
		},
		{
			"this vertical's worker logs under another domain", "enrichment",
			`package app

func New(deps Deps) {
	deps.Logger = sharedports.DomainLogger(slog.Default(), "watchdog")
}
`,
			false,
		},
		{
			"DomainLogger only named, never called", "enrichment",
			`package app

type DomainLogger struct{ domain string }

var _ = "enrichment"
`,
			false,
		},
		{
			"domain outside the closed AllowedDomains list", "b46_not_a_domain",
			`package app

func New(deps Deps) {
	deps.Logger = sharedports.DomainLogger(slog.Default(), "b46_not_a_domain")
}
`,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{f0advWorkerRel: tc.src})
			require.Equal(t, tc.want, f0DetectDomainLogger(t, root, tc.domain, f0advWorkerRel))
		})
	}
}

// --- grab record ------------------------------------------------------

func TestADR0025_F0_DetectorGrabRecordShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"positive control", `package grab

type Record struct {
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
}
`, true},
		{"SeasonNumber dropped", `package grab

type Record struct {
	SeriesID domain.SonarrSeriesID
}
`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{"internal/grab/domain/grab.go": tc.src})
			var hasSeries, hasSeason bool
			for _, f := range f0GrabRecordFields(t, root, f0RelGrabRecord) {
				switch f {
				case "SeriesID":
					hasSeries = true
				case "SeasonNumber":
					hasSeason = true
				}
			}
			require.Equal(t, tc.want, hasSeries && hasSeason)
		})
	}
}

// --- the seventh escape: rebind the cell to another vertical's file ---

// TestADR0025_F0_DetectorBindingRuleBites attacks f0BindingViolations, the rule
// that keeps a cell's probe pointed at ITS OWN vertical's code.
//
// The rule is new in this revision because the B-46 review reproduced the class
// a fourth time, one level above every function this file already hardens: with
// f0Detectors holding bare closures, moving the failure_journal/movie closure
// onto series_worker.go made the movie cell report Held while
// internal/enrichment/app/movie_worker.go was gutted. Every detector function
// was working exactly as designed — on the file it was handed. What was written
// down as "B-46 rule: no two cells may share a probe that does not mention
// either of them" was PROSE above a map, and prose does not fail a build.
//
// f0BindingViolations takes its map and its exception list as PARAMETERS for the
// sake of this test. A rule only ever run against the real, passing input cannot
// be told apart from a rule that always returns nothing — the same reason every
// case in this file is a pair.
//
// This test covers only the DECLARATIONS. Everything a cell's closure does with
// them is the other half, TestADR0025_F0_ProbeContractBites: the second review
// showed that a map which passes every case below can still be mis-bound in the
// body. Read the two together.
func TestADR0025_F0_DetectorBindingRuleBites(t *testing.T) {
	t.Parallel()

	journalSeries := verticals.Key{
		Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalSeries,
	}
	journalMovie := verticals.Key{
		Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalMovie,
	}
	yes := func(t *testing.T, root string, c f0Cell) bool { return true }

	cell := func(disc string, reads ...string) f0Cell {
		return f0Cell{Reads: reads, Discriminator: disc, Probe: yes}
	}

	cases := []struct {
		name      string
		cells     map[verticals.Key]f0Cell
		pathBlind map[verticals.Key]string
		// wantViolation false means the map is clean.
		wantViolation bool
		// contains, when set, must appear in the joined violation text — it
		// pins WHICH rule fired, so a case cannot pass on an unrelated one.
		contains string
	}{
		{
			name: "positive control: each cell reads its own vertical's worker",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("", f0RelSeriesWorker),
				journalMovie:  cell("", f0RelMovieWorker),
			},
		},
		{
			name: "THE vector: the movie cell is rebound to the series worker",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("", f0RelSeriesWorker),
				journalMovie:  cell("", f0RelSeriesWorker),
			},
			wantViolation: true,
			contains:      "failure_journal/movie",
		},
		{
			name: "both cells rebound onto one shared file, neither discriminated",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("", f0RelWiringEnrichment),
				journalMovie:  cell("", f0RelWiringEnrichment),
			},
			wantViolation: true,
			contains:      "the same reads AND the same discriminator",
		},
		{
			name: "one shared file, but the discriminator was copied across too",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("SourceTMDBSeries", f0RelWiringEnrichment),
				journalMovie:  cell("SourceTMDBSeries", f0RelWiringEnrichment),
			},
			pathBlind: map[verticals.Key]string{
				journalMovie: "movie is not in the path; the Source constant discriminates",
			},
			wantViolation: true,
			contains:      "the same reads AND the same discriminator",
		},
		{
			name: "ATTACK A2: both journal cells on the SERIES worker, told apart by a " +
				"discriminator",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("series", f0RelSeriesWorker),
				journalMovie:  cell("movie", f0RelSeriesWorker),
			},
			wantViolation: true,
			contains:      "never \"movie\"",
		},
		{
			name: "rule 2-bis on a single cell: the movie cell reads the series picker",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("SourceTMDBMovie", f0RelSeriesPicker),
			},
			wantViolation: true,
			contains:      "name the \"series\" vertical",
		},
		{
			name: "positive control for rule 2-bis: the cell reads BOTH verticals' files",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelMovieWorker, f0RelSeriesWorker),
			},
		},
		{
			name: "positive control for rule 2-bis: a foreign-token read, excused in writing",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelSeriesWorker),
			},
			pathBlind: map[verticals.Key]string{
				journalMovie: "the carrier really is the series worker; see the B-46 report",
			},
		},
		{
			name: "the retry_sweep shape: one shared file, two distinct discriminators",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("SourceTMDBSeries", f0RelWiringEnrichment),
				journalMovie:  cell("SourceTMDBMovie", f0RelWiringEnrichment),
			},
		},
		{
			name: "one shared file, only one side discriminated",
			cells: map[verticals.Key]f0Cell{
				journalSeries: cell("SourceTMDBSeries", f0RelWiringEnrichment),
				journalMovie:  cell("", f0RelWiringEnrichment),
			},
			pathBlind: map[verticals.Key]string{
				journalMovie: "movie is not in the path",
			},
			wantViolation: true,
			contains:      "needs a non-empty Discriminator",
		},
		{
			name: "a vertical-blind cell with no exception entry",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelWiringEnrichment),
			},
			wantViolation: true,
			contains:      "not bound to this vertical's code",
		},
		{
			name: "the same cell, excused in writing",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelWiringEnrichment),
			},
			pathBlind: map[verticals.Key]string{
				journalMovie: "the carrier is the shared wiring file; see the B-46 report",
			},
		},
		{
			name: "excused with an empty reason",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelWiringEnrichment),
			},
			pathBlind:     map[verticals.Key]string{journalMovie: "   "},
			wantViolation: true,
			contains:      "empty reason",
		},
		{
			name: "a stale exception on a cell that does name its vertical",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", f0RelMovieWorker),
			},
			pathBlind:     map[verticals.Key]string{journalMovie: "no longer true"},
			wantViolation: true,
			contains:      "delete the stale exception",
		},
		{
			name:          "a cell that declares no Reads at all",
			cells:         map[verticals.Key]f0Cell{journalMovie: {Discriminator: "movie", Probe: yes}},
			wantViolation: true,
			contains:      "declares no Reads",
		},
		{
			name: "a Reads entry that escapes the repo",
			cells: map[verticals.Key]f0Cell{
				journalMovie: cell("", "../../etc/movie_worker.go"),
			},
			wantViolation: true,
			contains:      "not a clean repo-relative slash path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := f0BindingViolations(tc.cells, tc.pathBlind)
			joined := strings.Join(got, "\n")
			if !tc.wantViolation {
				require.Emptyf(t, got, "expected a clean map, got:\n%s", joined)
				return
			}
			require.NotEmpty(t, got, "the rule accepted a mis-bound detector map")
			if tc.contains != "" {
				require.Containsf(t, joined, tc.contains,
					"a violation fired, but not the one this case is about:\n%s", joined)
			}
		})
	}
}

// --- the eighth escape: leave the declaration honest, look elsewhere --

// f0advCellSrc wraps map entries in a parseable file declaring an f0Cell map,
// so the probe-contract rule can be run against synthetic detector SOURCE. The
// fixtures never compile or type-check — the rule parses, it does not build.
func f0advCellSrc(entries string) string {
	return "package integration\n\nvar f0advDetectors = map[verticals.Key]f0Cell{\n" +
		entries + "}\n"
}

// f0advJournalMovieKey / f0advJournalSeriesKey are the key literals the fixtures
// use, spelled as SOURCE because that is what the rule reads.
const (
	f0advJournalMovieKey  = "\t{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalMovie}: "
	f0advJournalSeriesKey = "\t{Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalSeries}: "
)

// f0advBanned is the package-level value set the fixtures are judged against.
// In production the set is DERIVED from the package (f0PackageValueNames); here
// it is spelled, so a case says exactly which names it expects to be out of
// reach.
var f0advBanned = map[string]bool{
	"f0RelSeriesWorker":       true,
	"f0RelMovieWorker":        true,
	"f0RelWiringEnrichment":   true,
	"f0SearchMetricCallSites": true,
}

// TestADR0025_F0_ProbeContractBites attacks f0ProbeContractViolations, the half
// of the B-46 binding rule that reads the probe BODIES.
//
// It exists because the declaration half alone was walked around twice in the
// second review, with the suite green both times:
//
//   - H-1: Reads left honest at movie_worker.go, the body's one argument
//     changed from c.Reads[0] to f0RelSeriesWorker, movie_worker.go gutted. The
//     declaration rule reads declarations; the declaration was true.
//   - H-2: both failure_journal cells pointed at series_worker.go, told apart by
//     discriminators "series" / "movie" that neither body mentions. Rule 2 was
//     satisfied by the token being PRESENT in a string field, and rule 4 by the
//     two fields differing. The lock was not picked, it was stepped around.
//
// Both vectors are cases below, and both were reproduced on the live tree
// before being fixed (see the B-46 report): pre-fix GREEN, post-fix RED.
//
// There is deliberately NO exception list for this rule. The two multi-file
// probes that could not satisfy it — the metrics_namespace pair, whose paths
// used to come from package-level site lists — were brought to the contract
// instead (f0MetricSitesSatisfied now opens the CELL's paths and fails loudly if
// the site list and the declaration disagree). An exemption would have been the
// third soft spot in a mechanism written against soft spots.
func TestADR0025_F0_ProbeContractBites(t *testing.T) {
	t.Parallel()

	const honestMovie = `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`
	const honestSeries = `{
		Reads: []string{f0RelSeriesWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`

	cases := []struct {
		name          string
		entries       string
		wantViolation bool
		contains      string
	}{
		{
			name:    "positive control: both cells take their file from their own cell",
			entries: f0advJournalSeriesKey + honestSeries + f0advJournalMovieKey + honestMovie,
		},
		{
			name: "H-1: honest Reads, body hardcodes the other vertical's path constant",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, f0RelSeriesWorker)
		},
	},
`,
			wantViolation: true,
			contains:      "names the package-level value f0RelSeriesWorker",
		},
		{
			name: "the same rebinding written as a bare string literal",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, "internal/enrichment/app/series_worker.go")
		},
	},
`,
			wantViolation: true,
			contains:      "contains the string literal",
		},
		{
			name: "a marker literal smuggled into the body instead of Markers",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectDomainLogger(t, root, "enrichment", c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "contains the string literal",
		},
		{
			name: "H-2: a Discriminator the body never consumes",
			entries: f0advJournalMovieKey + `{
		Reads:         []string{f0RelMovieWorker},
		Discriminator: "movie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "declares Discriminator but the probe never consumes c.Discriminator",
		},
		{
			name: "H-2 combined: two cells on ONE foreign file, told apart by decoration",
			entries: f0advJournalSeriesKey + `{
		Reads:         []string{f0RelSeriesWorker},
		Discriminator: "series",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
` + f0advJournalMovieKey + `{
		Reads:         []string{f0RelSeriesWorker},
		Discriminator: "movie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "never consumes c.Discriminator",
		},
		{
			name: "ATTACK A2's other half: the Discriminator is consumed by assigning it to _",
			entries: f0advJournalMovieKey + `{
		Reads:         []string{f0RelMovieWorker},
		Discriminator: "movie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			_ = c.Discriminator
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "assigns c.Discriminator to the blank identifier",
		},
		{
			name: "the same vacuous form on Reads",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			_ = c.Reads
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "assigns c.Reads to the blank identifier",
		},
		{
			name: "the same vacuous form on Markers",
			entries: f0advJournalMovieKey + `{
		Reads:   []string{f0RelMovieWorker},
		Markers: []string{"enrichment"},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			_ = c.Markers
			return f0DetectDomainLogger(t, root, c.Markers[0], c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "assigns c.Markers to the blank identifier",
		},
		{
			name: "Markers declared and never read",
			entries: f0advJournalMovieKey + `{
		Reads:   []string{f0RelMovieWorker},
		Markers: []string{"enrichment"},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "declares Markers but the probe never consumes c.Markers",
		},
		{
			name: "a Marker consumed but never declared — the probe reads a zero value",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectDomainLogger(t, root, c.Markers[0], c.Reads[0])
		},
	},
`,
			wantViolation: true,
			contains:      "which this cell does not declare",
		},
		{
			name: "the shape both metrics probes shipped with: the cell is discarded",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, _ f0Cell) bool {
			return f0DetectEnrichmentMetrics(t, root, f0SearchMetricCallSites)
		},
	},
`,
			wantViolation: true,
			contains:      "does not name its f0Cell parameter",
		},
		{
			name: "Reads declared, but the body opens whatever a helper decides",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, f0DefaultWorkerRel())
		},
	},
`,
			wantViolation: true,
			contains:      "never consumes c.Reads",
		},
		{
			name: "a constant borrowed from another package",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0RetrySweepHasSource(t, root, c.Reads[0], enrichdomain.SourceTMDBSeries)
		},
	},
`,
			wantViolation: true,
			contains:      "reaches outside its cell",
		},
		{
			name: "Probe is a shared function, so no body is visible here at all",
			entries: f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: f0SomeSharedProbe,
	},
`,
			wantViolation: true,
			contains:      "not a function literal",
		},
		{
			name: "the retry_sweep shape: one file, two discriminators, both consumed",
			entries: f0advJournalSeriesKey + `{
		Reads:         []string{f0RelWiringEnrichment},
		Discriminator: "SourceTMDBSeries",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0RetrySweepHasSource(t, root, c.Reads[0], c.Discriminator)
		},
	},
` + f0advJournalMovieKey + `{
		Reads:         []string{f0RelWiringEnrichment},
		Discriminator: "SourceTMDBMovie",
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0RetrySweepHasSource(t, root, c.Reads[0], c.Discriminator)
		},
	},
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := f0ProbeContractViolations(t, f0advCellSrc(tc.entries), "f0advDetectors",
				f0advBanned)
			joined := strings.Join(got, "\n")
			if !tc.wantViolation {
				require.Emptyf(t, got, "expected a clean detector source, got:\n%s", joined)
				return
			}
			require.NotEmpty(t, got, "the contract accepted a probe that does not take its "+
				"inputs from its cell")
			if tc.contains != "" {
				require.Containsf(t, joined, tc.contains,
					"a violation fired, but not the one this case is about:\n%s", joined)
			}
		})
	}
}

// TestADR0025_F0_ThreeRulesAreOneSystem pins that each of the three rules
// guarding a detector cell catches something the other two do not, so none of
// them can be deleted as "already covered".
//
// The three rules, and the escape each one owns:
//
//   - f0BindingViolations reads the DECLARATIONS. Owns attack A2: two cells on
//     ONE vertical's file, told apart by a discriminator. Until review #3 this
//     rule accepted that shape — the token was PRESENT in a string field — and
//     the case below used to assert the rule stayed silent. It no longer does:
//     rule 2-bis fires on a cell that reads another vertical's source and none
//     of its own, whatever the discriminator says. The case is kept, inverted,
//     because an inverted expectation is the clearest possible record that the
//     rule changed.
//   - f0ProbeContractViolations reads the BODIES. Owns H-1: Reads left honest,
//     the body's one argument replaced with the other vertical's path constant.
//     Nothing in the declaration is wrong, so the first rule is silent.
//   - f0ProbeOpenViolations watches what f0Path actually RESOLVED. Owns attack
//     D: a second, real path added to Reads and c.Reads[1] opened in the body.
//     The declaration is a clean two-file cell; the body takes its path from the
//     cell and consumes c.Reads exactly as the contract demands. Both text rules
//     are silent, and only the run-time record of which file was opened tells
//     the two indices apart.
func TestADR0025_F0_ThreeRulesAreOneSystem(t *testing.T) {
	t.Parallel()

	journalSeries := verticals.Key{
		Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalSeries,
	}
	journalMovie := verticals.Key{
		Invariant: verticals.InvariantFailureJournal, Vertical: verticals.VerticalMovie,
	}

	t.Run("A2 is caught by the DECLARATION rule alone", func(t *testing.T) {
		t.Parallel()
		probe := func(t *testing.T, root string, c f0Cell) bool { return true }
		mis := map[verticals.Key]f0Cell{
			journalSeries: {
				Reads: []string{f0RelSeriesWorker}, Discriminator: "series", Probe: probe,
			},
			journalMovie: {
				Reads: []string{f0RelSeriesWorker}, Discriminator: "movie", Probe: probe,
			},
		}
		got := f0BindingViolations(mis, nil)
		require.NotEmpty(t, got,
			"before review #3 this map was ACCEPTED by the declaration rule, which is how a "+
				"gutted movie_worker.go reported Held; rule 2-bis is what changed")
		require.Contains(t, strings.Join(got, "\n"), "never \"movie\"")
	})

	t.Run("H-1 is invisible to the declaration rule, caught by the BODY rule", func(t *testing.T) {
		t.Parallel()
		honest := map[verticals.Key]f0Cell{
			journalMovie: {
				Reads: []string{f0RelMovieWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool { return true },
			},
		}
		require.Empty(t, f0BindingViolations(honest, nil),
			"this is the point of the case: the DECLARATION is impeccable — the movie cell "+
				"declares the movie worker and nothing else")

		src := f0advCellSrc(f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, f0RelSeriesWorker)
		},
	},
`)
		require.NotEmpty(t, f0ProbeContractViolations(t, src, "f0advDetectors", f0advBanned),
			"the BODY rule is what catches it — without this half the H-1 walk-around is green")
	})

	t.Run("D is invisible to BOTH text rules, caught by the OPENED-SET rule", func(t *testing.T) {
		t.Parallel()
		attackD := map[verticals.Key]f0Cell{
			journalMovie: {
				Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					return f0DetectFailureJournal(t, root, c.Reads[1])
				},
			},
		}
		require.Empty(t, f0BindingViolations(attackD, nil),
			"this is the point of the case: the DECLARATION rule sees a two-file cell whose "+
				"reads include its own vertical's worker, which is an entirely ordinary shape")

		src := f0advCellSrc(f0advJournalMovieKey + `{
		Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
		Probe: func(t *testing.T, root string, c f0Cell) bool {
			return f0DetectFailureJournal(t, root, c.Reads[1])
		},
	},
`)
		require.Empty(t, f0ProbeContractViolations(t, src, "f0advDetectors", f0advBanned),
			"and the point of the second half of the case: the BODY rule sees a closure that "+
				"contains no literal, names no package value, and consumes c.Reads. "+
				"c.Reads[0] and c.Reads[1] are the same text shape — no rule that reads text "+
				"can tell them apart")

		root := f0advFixture(t, map[string]string{
			f0RelMovieWorker:  f0advJournalHelperNeverCalled,
			f0RelSeriesWorker: f0advJournalGood,
		})
		cell := attackD[journalMovie]
		held, opened := f0RunProbe(t, root, cell)
		require.True(t, held,
			"the whole danger of attack D: with the movie worker gutted the probe still "+
				"answers Held, because it read the series worker")
		got := f0ProbeOpenViolations("failure_journal/movie", cell.Reads, opened)
		require.NotEmpty(t, got,
			"the OPENED-SET rule is the only one left that can see it")
		require.Contains(t, strings.Join(got, "\n"), "never opened it")
	})
}

// --- the ninth escape: declare two paths, open the other one ----------

// TestADR0025_F0_ProbeOpenSetBites attacks f0ProbeOpenViolations, the rule that
// compares a cell's DECLARED reads with the paths its probe actually resolved.
//
// It is the third and youngest of the three guards, and it exists because the
// other two are both text rules. Attack D, from review #3:
//
//	Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
//	Probe: func(t *testing.T, root string, c f0Cell) bool {
//	        return f0DetectFailureJournal(t, root, c.Reads[1])
//	},
//
// The declaration names the movie worker, so the binding rule is content. The
// body contains no literal, names no package value and consumes c.Reads, so the
// probe contract is content. The whole lie is the index, and no rule that reads
// source text can see it — `c.Reads[0]` and `c.Reads[1]` are the same shape.
// Reproduced on the live tree before the fix: three w.recordEnrichmentError call
// sites removed from internal/enrichment/app/movie_worker.go, this cell in
// place, `go test -run TestADR0025` GREEN.
//
// The rule's BOUNDARY is a case here too, with wantViolation false. Every attack
// case above is written lazily — `c.Reads[1]` reached directly, the other rel
// never resolved — and that is the only form the rule sees. A multi-file probe
// following the prescription in f0Paths resolves its whole declaration up front,
// so opened ≡ declared holds identically and the swap goes through. Review #4
// found the set proving the rule against a spelling the file elsewhere forbids;
// the boundary case is here so the fifth reviewer meets it as a DECLARED trade
// rather than discovering it a sixth time.
//
// Every case runs against a synthetic two-file tree where the MOVIE worker is
// gutted and the SERIES worker is whole, so a probe that reads the wrong one
// answers true and a rule that did nothing would be visibly useless.
func TestADR0025_F0_ProbeOpenSetBites(t *testing.T) {
	t.Parallel()

	const label = "failure_journal/movie"

	cases := []struct {
		name          string
		cell          f0Cell
		wantHeld      bool
		wantViolation bool
		contains      string
	}{
		{
			name: "positive control: one declared path, and the probe opens it",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					return f0DetectFailureJournal(t, root, c.Reads[0])
				},
			},
			wantHeld: false,
		},
		{
			name: "positive control: two declared paths, and the probe opens both",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					a := f0DetectFailureJournal(t, root, c.Reads[0])
					b := f0DetectFailureJournal(t, root, c.Reads[1])
					return a && b
				},
			},
			wantHeld: false,
		},
		{
			name: "ATTACK D: two declared paths, the body opens c.Reads[1]",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					return f0DetectFailureJournal(t, root, c.Reads[1])
				},
			},
			wantHeld:      true,
			wantViolation: true,
			contains:      "never opened it",
		},
		{
			name: "the prescribed up-front resolution deliberately does NOT catch the " +
				"index swap",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker, f0RelSeriesWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					// Exactly what f0Paths PRESCRIBES for a multi-file probe,
					// paired with the one-character swap of ATTACK D above. The
					// whole declaration is resolved, so opened equals declared
					// and this rule is a tautology here — while the body reads
					// the series worker under the movie cell's name and the
					// gutted movie worker goes unexamined.
					f0Paths(t, root, c.Reads)
					return f0DetectFailureJournal(t, root, c.Reads[1])
				},
			},
			wantHeld:      true,
			wantViolation: false,
		},
		{
			name: "the same rule from the other side: a declared path nothing ever opens",
			cell: f0Cell{
				Reads: []string{f0RelSeriesWorker, f0RelMovieWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					return f0DetectFailureJournal(t, root, c.Reads[0])
				},
			},
			wantHeld:      true,
			wantViolation: true,
			contains:      "internal/enrichment/app/movie_worker.go but the probe never opened",
		},
		{
			name: "a path opened that the cell never declared",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					_ = c.Reads
					return f0DetectFailureJournal(t, root, f0RelSeriesWorker)
				},
			},
			wantHeld:      true,
			wantViolation: true,
			contains:      "which the cell does not declare in Reads",
		},
		{
			name: "a probe that opens nothing at all",
			cell: f0Cell{
				Reads: []string{f0RelMovieWorker},
				Probe: func(t *testing.T, root string, c f0Cell) bool {
					return len(c.Reads) > 0
				},
			},
			wantHeld:      true,
			wantViolation: true,
			contains:      "opened no file at all",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := f0advFixture(t, map[string]string{
				f0RelMovieWorker:  f0advJournalHelperNeverCalled,
				f0RelSeriesWorker: f0advJournalGood,
			})
			held, opened := f0RunProbe(t, root, tc.cell)
			require.Equalf(t, tc.wantHeld, held,
				"the fixture is built so a probe reading the MOVIE worker answers false and "+
					"one reading the SERIES worker answers true; this case expected %v",
				tc.wantHeld)

			got := f0ProbeOpenViolations(label, tc.cell.Reads, opened)
			joined := strings.Join(got, "\n")
			if !tc.wantViolation {
				require.Emptyf(t, got, "expected a clean cell, got:\n%s", joined)
				// Two kinds of case land here: honest probes, and the declared
				// boundary — a probe that resolves its whole declaration up
				// front and then reads the wrong index. If the boundary case
				// ever starts producing a violation, that is a WIN and this
				// case is what must be rewritten, together with the remainder
				// list in the f0Cell docblock.
				return
			}
			require.NotEmpty(t, got, "the rule accepted a probe that did not read its own "+
				"declaration")
			if tc.contains != "" {
				require.Containsf(t, joined, tc.contains,
					"a violation fired, but not the one this case is about:\n%s", joined)
			}
		})
	}
}

// --- the sixth escape: relocate the cell, delete the detector ---------

// TestADR0025_F0_UndetectableEscapeStaysLocked is the explicit cross-reference
// to TestADR0025_F0_UndetectableSetIsLocked in
// adr0025_f0_vertical_parity_test.go.
//
// Every detector this file hardens can still be neutralised without touching a
// single line of detector code: move the cell into f0Undetectable with a
// well-worded reason and delete its entry from f0Detectors. The
// exactly-one-of-detector/undetectable/deferred-guard rule in
// TestADR0025_F0_RegistryMatchesCode does NOT catch that — the relocated cell is
// resolved by exactly one thing, the undetectable entry. What catches it is the
// hard count lock: growing f0Undetectable past one entry fails, so the escape
// cannot be taken silently, only in a diff that says out loud why an honest
// probe became impossible.
//
// The assertion is repeated here rather than merely referenced in prose so that
// deleting the lock test shows up as a failure in the adversarial suite too —
// the two guards are one system and must not be removable one at a time.
func TestADR0025_F0_UndetectableEscapeStaysLocked(t *testing.T) {
	t.Parallel()
	require.Lenf(t, f0Undetectable, 1,
		"the escape hatch from every detector in this file is f0Undetectable. Its size is "+
			"locked by TestADR0025_F0_UndetectableSetIsLocked and re-asserted here: a cell "+
			"moved in here stops being probed at all, so the set may only grow in a change "+
			"that bumps BOTH counts and states why an honest probe is impossible "+
			"(ADR-0025 R2-bis)")
}
