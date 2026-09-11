// Package verticals is the ADR-0025 vertical-parity invariant registry.
//
// It is a DECLARATION, not a mechanism. For every (invariant, vertical)
// pair it states whether the codebase carries that invariant TODAY.
// Conformance tests in tests/integration/ assert the declaration against
// the real code and go red when the two diverge in EITHER direction — a
// Held the code lost, or a Gap the code silently closed. A registry that
// only went red in one direction would rot: today's Gap would stay
// declared long after some phase closed it, and the next vertical would
// inherit a lie.
//
// Three states, not two (ADR-0025 R2-bis). "Held" and "Gap" cannot
// describe something deliberately postponed; without a third value a
// postponed item would have to lie as Held or be dropped from the
// registry — and dropping it is the exact disease this ADR treats.
//
// ZERO internal imports (ADR-0025 R3). This package is shared kernel: it
// must never import application / domain / infrastructure / interface or
// any other internal package, otherwise the registry would depend on the
// very code it describes. Enforced twice:
//   - tests/lint_shared_verticals_imports_test.go (lint build tag, run by
//     `make test-lint-rule`),
//   - TestADR0025_F0_VerticalsPackageHasNoInternalImports
//     (integration build tag — the one CI actually executes; the lint job
//     in .github/workflows/ci.yml runs golangci-lint only and never
//     invokes the lint-tagged suite).
package verticals

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
)

// Vertical is a content vertical that can carry an invariant. The set is
// closed and matches the columns of the ADR-0025 R4 table.
type Vertical string

const (
	VerticalSeries Vertical = "series"
	VerticalMovie  Vertical = "movie"
)

// AllVerticals is the closed column set of the R4 table.
var AllVerticals = []Vertical{VerticalSeries, VerticalMovie}

// Invariant is a property the first vertical established and every later
// vertical is expected to carry (or to explicitly defer). The set is
// closed and matches the rows of the ADR-0025 R4 table.
type Invariant string

const (
	// InvariantFailureJournal — the worker writes an enrichment_errors
	// row when a source fails.
	InvariantFailureJournal Invariant = "failure_journal"
	// InvariantPickerBreaker — the refresh picker excludes entities whose
	// journalled attempts crossed the terminal threshold.
	InvariantPickerBreaker Invariant = "picker_breaker"
	// InvariantRetrySweep — journalled non-terminal failures are swept
	// back into the queue, so the journal is not write-only.
	InvariantRetrySweep Invariant = "retry_sweep"
	// InvariantLoopDeclaresTypes — a consumer of the shared, type-neutral
	// loop spawner declares which instance types it supports.
	InvariantLoopDeclaresTypes Invariant = "loop_declares_types"
	// InvariantRegrabSupported — the vertical can re-grab a dead or
	// updated torrent.
	InvariantRegrabSupported Invariant = "regrab_supported"
	// InvariantMetricsNamespace — the bounded context exports at least one
	// metric family.
	InvariantMetricsNamespace Invariant = "metrics_namespace"
	// InvariantDomainLogger — the domain is registered in the closed
	// AllowedDomains list.
	InvariantDomainLogger Invariant = "domain_logger"
)

// AllInvariants is the closed row set of the R4 table.
var AllInvariants = []Invariant{
	InvariantFailureJournal,
	InvariantPickerBreaker,
	InvariantRetrySweep,
	InvariantLoopDeclaresTypes,
	InvariantRegrabSupported,
	InvariantMetricsNamespace,
	InvariantDomainLogger,
}

// State is the three-valued declaration of one registry cell.
type State string

const (
	// StateHeld — the invariant is carried. The conformance test requires
	// the code to carry it.
	StateHeld State = "held"
	// StateGap — a hole that this epic closes. The conformance test
	// requires the code to NOT carry it: a Gap the code already closed is
	// a stale declaration and fails just as loudly as a lost Held.
	StateGap State = "gap"
	// StateDeferred — deliberately postponed or not applicable by domain,
	// with a reason and a reference. The conformance test asserts nothing
	// about Held/Gap, but the reason MUST be non-empty and the premise of
	// the deferral is guarded separately so it cannot rot in silence.
	StateDeferred State = "deferred"
)

// IsValid reports whether s is one of the three known states.
func (s State) IsValid() bool {
	return s == StateHeld || s == StateGap || s == StateDeferred
}

// Key addresses one cell of the R4 table.
type Key struct {
	Invariant Invariant
	Vertical  Vertical
}

// String renders the key as "invariant/vertical" for test failure output.
func (k Key) String() string { return string(k.Invariant) + "/" + string(k.Vertical) }

// Status is the declaration attached to one cell.
type Status struct {
	// State is the three-valued declaration.
	State State
	// Reason explains the declaration in prose. MANDATORY for
	// StateDeferred (an empty reason is a validation error — that is what
	// keeps a postponed item from pretending to be either done or
	// forgotten). Optional for Held / Gap.
	Reason string
	// Evidence is the file:line anchor an auditor follows to check the
	// declaration by hand. Mandatory for every cell.
	Evidence string
	// ClosedBy names the phase that closes a Gap. Mandatory for StateGap,
	// and MUST be empty for StateHeld (nothing left to close).
	ClosedBy string
	// Subject names what actually carries the invariant when the carrier
	// is not the vertical itself. Empty means "the vertical itself".
	//
	// The one cell that needs it today is metrics_namespace/movie: the
	// ADR-0025 R4 table writes "search" into that cell, because the movie
	// vertical DOES export metrics (seasonfill_movie_refresh_*,
	// seasonfill_movie_changes_*) while the search bounded context shipped
	// with none. Declaring that cell as a movie-vertical gap and probing
	// for seasonfill_movie_ would be a lie the conformance test would
	// immediately expose. Subject keeps the declaration honest without
	// inventing a third Vertical.
	Subject string
	// Note carries any additional prose the auditor needs.
	Note string
}

// Entry is one registry row, returned by All.
type Entry struct {
	Key    Key
	Status Status
}

// registry is the ADR-0025 R4 table, verbatim. Every (invariant,
// vertical) pair MUST be present — completeness is enforced by validate,
// so adding an Invariant or a Vertical constant without declaring its
// cells fails the unit test.
var registry = map[Key]Status{
	{InvariantFailureJournal, VerticalSeries}: {
		State: StateHeld,
		Evidence: "internal/enrichment/app/series_worker.go:107 (EnrichmentErrors dep), " +
			":1732,:1748,:1759 (handleTMDBError arms), :1775 (recordEnrichmentError)",
		Reason: "SeriesWorker journals every TMDB failure; a 404 parks the row terminally " +
			"(terminalAttempts, NextAttemptAt nil), anything else gets a backoff.",
	},
	{InvariantFailureJournal, VerticalMovie}: {
		State: StateHeld,
		Evidence: "internal/enrichment/app/movie_worker.go:96 (EnrichmentErrors dep), " +
			":470 (handleTMDBError 404 / backoff / park arms), " +
			":523 (recordEnrichmentError → EnrichmentErrors.RecordFailure), " +
			":550 (clearEnrichmentError on a committed hydrate)",
		Reason: "MovieWorker.HandleForced journals every failure of the /movie/{id} fetch: a " +
			"404 parks the row terminally (terminalAttempts, NextAttemptAt nil) so the seven " +
			"TMDB-deleted movies of ADR-0025 Proof #2 are never re-pulled; anything else lands " +
			"as previousAttempts+1 with the shared enrichment.NextAttemptAt backoff, parked " +
			"terminally once ShouldPark trips (E-FIX-1 parity on the fetch path). A " +
			"committed hydrate clears the row, so the ledger cannot become a permanent " +
			"breaker. One deliberate difference from the series helper: this one does NOT " +
			"swallow the error — HandleForced still returns it, so the movie_refresh " +
			"ok/error accounting is unchanged.",
	},
	{InvariantPickerBreaker, VerticalSeries}: {
		State: StateHeld,
		Evidence: "internal/enrichment/persistence/series_refresh_query.go:171,223,271,317,363 " +
			"— NOT EXISTS(enrichment_errors … ee.attempts > 5) in all five tier arms",
	},
	{InvariantPickerBreaker, VerticalMovie}: {
		State: StateHeld,
		Evidence: "internal/enrichment/persistence/movie_refresh_query.go:193," +
			"212 — NOT EXISTS(enrichment_errors … ee.attempts > 5) in both " +
			"tier arms (CHANGED tier 0, NORMAL tier 3)",
		Reason: "Both arms of the two-arm movie picker carry the same terminal-failure gate " +
			"the five series arms carry, bound to entity_type='movie' / source='tmdb_movie'. " +
			"Combined with the F1 journal a TMDB-deleted movie is journalled once at " +
			"attempts=99 and never re-picked, closing ADR-0025 Proof #2's ~15-minute re-pull " +
			"loop.",
	},
	{InvariantRetrySweep, VerticalSeries}: {
		State:    StateHeld,
		Evidence: "internal/wiring/enrichment.go:947 — ListDueForRetry(SourceTMDBSeries)",
	},
	{InvariantRetrySweep, VerticalMovie}: {
		State: StateHeld,
		Evidence: "internal/wiring/enrichment.go:996 — " +
			"ListDueForRetry(SourceTMDBMovie) → Dispatcher.Enqueue(EntityMovie, PriorityCold)",
		Reason: "Journalled non-terminal movie failures are swept back into the queue by the " +
			"nightly tick, so the F1 journal is not write-only. EntityMovie is a live lane: " +
			"DispatcherImpl.movieLoop drains it into MovieWorker.HandleForced. There is " +
			"deliberately NO movie stale-scan arm — movie staleness is owned by the " +
			"MovieRefreshScheduler's own 30-minute tiered picker, not by the nightly job.",
	},
	{InvariantLoopDeclaresTypes, VerticalSeries}: {
		State: StateHeld,
		Evidence: "internal/wiring/bootstrap.go — refreshQbitLoops godoc (\"the SPAWNER stays " +
			"type-neutral; each CONSUMER declares the instance types it supports\"); " +
			"cmd/server/loops/torrentsync.go — SwapSettings gates on Enabled only, never on " +
			"instance type, and torrentsync runs against the radarr instance in production",
		Note: "STILL not statically detectable after F2. F2 made the MOVIE side detectable by " +
			"giving regrab an explicit declaration; torrentsync declares nothing because it " +
			"genuinely supports every type, and \"declares nothing\" is indistinguishable from " +
			"\"forgot to declare\" in a source scan. The ADR therefore keeps grounding this cell " +
			"in live prod observation (torrentsync_reconciler_start/_done with instance=radarr " +
			"every ~30s, no errors). Any source scan here would be an imitation, so the " +
			"conformance test declares this cell undetectable instead of faking a probe.",
	},
	{InvariantLoopDeclaresTypes, VerticalMovie}: {
		State: StateHeld,
		Evidence: "internal/watchdog/app/regrab/instance_types.go:31,42 — SupportedInstanceTypes / " +
			"SupportsInstanceType; cmd/server/loops/regrab.go:295 (unsupportedLocked) and :235 " +
			"(the regrab_skipped_unsupported_type INFO in SwapSettings); " +
			"cmd/server/adapters/regrab_instance_types.go:53 — RegrabInstanceTypes.InstanceTypes " +
			"(sonarr + radarr holder union); internal/wiring/watchdog.go:250 — WithInstanceTypes",
		Reason: "regrab now DECLARES the arr_instance.type values it supports (sonarr only) " +
			"instead of consuming the shared type-neutral spawner blindly. A radarr row in " +
			"qbit_settings no longer spawns a per-instance loop that fails every 30 minutes " +
			"with regrab_iteration_failed; it is skipped with one INFO per transition and the " +
			"seasonfill_regrab_unresolved_instance gauge. The gate is fail-OPEN by " +
			"construction — an unresolvable type spawns the loop exactly as before F2, so a " +
			"boot-order race can never silently disable regrab in production. The radarr row " +
			"itself is untouched: torrentsync still consumes the same projection and still " +
			"runs against radarr.",
	},
	{InvariantRegrabSupported, VerticalSeries}: {
		State: StateHeld,
		Evidence: "internal/grab/domain/grab.go:58,60 — Record is keyed by " +
			"SeriesID + SeasonNumber; watchdog_state composite PK " +
			"(instance, sonarr_series_id, season_number)",
		Reason: "Re-grabbing a dead season torrent is seasonfill's core value on top of " +
			"Sonarr.",
	},
	{InvariantRegrabSupported, VerticalMovie}: {
		State: StateDeferred,
		Reason: "No movie-grab infrastructure exists at all: internal/grab/domain/grab.go:58,60 " +
			"key a grab Record by SeriesID + SeasonNumber with no movie field, and " +
			"internal/grab/domain/download_link.go:35-36 states \"Phase 1 always emits Sonarr " +
			"rows; ExternalMovieID stays nil\". Building movie-regrab means building " +
			"movie-grabbing from scratch — a new feature duplicating Radarr, not a parity fix. " +
			"Wanted in the future (track an updated release, re-pull via Radarr); parked as " +
			"backlog item B-41 (movie-regrab). Regrab stays series-only for now.",
		Evidence: "internal/grab/domain/grab.go:58,60; internal/grab/domain/download_link.go:35-36",
		Note: "Deferred, NOT a Gap: the conformance test asserts nothing about Held/Gap here, " +
			"but it does guard the premise — if a movie field ever appears on grab.Record, the " +
			"deferral has rotted and must be revisited.",
	},
	{InvariantMetricsNamespace, VerticalSeries}: {
		State:    StateHeld,
		Subject:  "enrichment bounded context",
		Evidence: "internal/observability/enrichment_refresh_metrics.go:27 — seasonfill_enrichment_refresh_*",
	},
	{InvariantMetricsNamespace, VerticalMovie}: {
		State:    StateGap,
		ClosedBy: "F3",
		Subject:  "search bounded context (ADR-0024, internal/search)",
		Evidence: "internal/observability/ — zero seasonfill_search_* literals; prod exports " +
			"165 distinct seasonfill_* families and none of them is a search family",
		Reason: "The movie VERTICAL does export metrics (seasonfill_movie_refresh_*, " +
			"seasonfill_movie_changes_*). The bounded context that shipped without any is " +
			"search — which is exactly what the ADR-0025 R4 table records in this cell " +
			"(\"search F3\"). Subject names the real carrier so the probe matches the claim " +
			"instead of testing a vertical that already holds the invariant.",
	},
	{InvariantDomainLogger, VerticalSeries}: {
		State: StateHeld,
		Evidence: "internal/shared/ports/log.go — \"enrichment\" in AllowedDomains; " +
			"internal/enrichment/app/series_worker.go:208",
	},
	{InvariantDomainLogger, VerticalMovie}: {
		State: StateHeld,
		Evidence: "internal/shared/ports/log.go — \"enrichment\" in AllowedDomains; " +
			"internal/enrichment/app/movie_worker.go:105",
		Note: "Both verticals log under the same domain value; the closed AllowedDomains " +
			"list plus its wiring-time panic is the one mechanism in this codebase with a " +
			"proven record against this bug class (ADR-0025 Proof #4).",
	},
}

// SourceException is a Source constant that is deliberately NOT accepted
// by enrichment.Source.IsValid().
//
// ADR-0025 R4-bis: the invariant is "every JOURNALLED source must pass
// IsValid()", not "every Source constant must". SourceSonarr / SourceQbit
// are live-reachability flags, not journal discriminators, and the code
// says so itself — Quote below is asserted to be literally present in
// degraded.go, so the citation cannot rot.
type SourceException struct {
	// Source is the string value of the excepted Source constant.
	Source string
	// Reason explains why the exception is correct.
	Reason string
	// Quote is a verbatim substring of the in-code documentation that
	// establishes the exception.
	Quote string
	// Evidence is the file:line anchor.
	Evidence string
}

// JournalledSourceExceptions lists the Source constants that MUST NOT
// pass IsValid(). Anything not listed here must pass.
var JournalledSourceExceptions = []SourceException{
	{
		Source: "sonarr",
		Reason: "live reachability flag surfaced through degraded[]; never persisted to " +
			"enrichment_errors, so IsValid() — which guards persistence — must reject it",
		Quote:    "queried per-request, not journalled",
		Evidence: "internal/enrichment/domain/enrichment/degraded.go:5-9,11",
	},
	{
		Source: "qbit",
		Reason: "live reachability flag surfaced through degraded[]; never persisted to " +
			"enrichment_errors, so IsValid() — which guards persistence — must reject it",
		Quote:    "queried per-request, not journalled",
		Evidence: "internal/enrichment/domain/enrichment/degraded.go:5-9,12",
	},
}

// IsJournalledSourceException reports whether the raw Source value is a
// declared known-exception to the "journalled source must pass IsValid()"
// invariant.
func IsJournalledSourceException(source string) bool {
	for _, e := range JournalledSourceExceptions {
		if e.Source == source {
			return true
		}
	}
	return false
}

// Lookup returns the declared status of one cell.
func Lookup(inv Invariant, v Vertical) (Status, bool) {
	s, ok := registry[Key{Invariant: inv, Vertical: v}]
	return s, ok
}

// MustLookup is Lookup for callers that treat a missing cell as a bug.
func MustLookup(inv Invariant, v Vertical) Status {
	s, ok := Lookup(inv, v)
	if !ok {
		panic(fmt.Sprintf("verticals: no declaration for %s", Key{Invariant: inv, Vertical: v}))
	}
	return s
}

// All returns every registry row, sorted by (invariant, vertical) so test
// output and audit diffs are stable.
func All() []Entry {
	out := make([]Entry, 0, len(registry))
	for k, s := range registry {
		out = append(out, Entry{Key: k, Status: s})
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if c := cmp.Compare(a.Key.Invariant, b.Key.Invariant); c != 0 {
			return c
		}
		return cmp.Compare(a.Key.Vertical, b.Key.Vertical)
	})
	return out
}

// Validate reports every structural defect in the declaration. An empty
// result means the registry is well-formed; it says NOTHING about whether
// the code matches it — that is the conformance test's job.
func Validate() []error { return validate(registry, JournalledSourceExceptions) }

// validate is the testable core of Validate. Kept separate so the unit
// test can feed it deliberately broken tables without mutating the real
// registry.
func validate(reg map[Key]Status, exceptions []SourceException) []error {
	var errs []error

	knownInv := make(map[Invariant]bool, len(AllInvariants))
	for _, i := range AllInvariants {
		knownInv[i] = true
	}
	knownVert := make(map[Vertical]bool, len(AllVerticals))
	for _, v := range AllVerticals {
		knownVert[v] = true
	}

	for k, s := range reg {
		if !knownInv[k.Invariant] {
			errs = append(errs, fmt.Errorf("%s: unknown invariant %q", k, k.Invariant))
		}
		if !knownVert[k.Vertical] {
			errs = append(errs, fmt.Errorf("%s: unknown vertical %q", k, k.Vertical))
		}
		if !s.State.IsValid() {
			errs = append(errs, fmt.Errorf("%s: unknown state %q", k, s.State))
		}
		if s.Evidence == "" {
			errs = append(errs, fmt.Errorf("%s: evidence must not be empty", k))
		}
		switch s.State {
		case StateDeferred:
			if s.Reason == "" {
				errs = append(errs, fmt.Errorf(
					"%s: deferred without a reason — a postponed invariant must say why "+
						"and where it is tracked (ADR-0025 R2-bis)", k))
			}
			if s.ClosedBy != "" {
				errs = append(errs, fmt.Errorf(
					"%s: deferred cells must not name a closing phase (got %q) — "+
						"deferral means no phase of this epic closes it", k, s.ClosedBy))
			}
		case StateGap:
			if s.ClosedBy == "" {
				errs = append(errs, fmt.Errorf(
					"%s: gap without a closing phase — declare which phase closes it "+
						"or declare it deferred with a reason", k))
			}
		case StateHeld:
			if s.ClosedBy != "" {
				errs = append(errs, fmt.Errorf(
					"%s: held cells must not name a closing phase (got %q)", k, s.ClosedBy))
			}
		}
	}

	// Completeness: the table is a full cross product. A new Invariant or
	// Vertical constant without declared cells fails here.
	for _, i := range AllInvariants {
		for _, v := range AllVerticals {
			if _, ok := reg[Key{Invariant: i, Vertical: v}]; !ok {
				errs = append(errs, fmt.Errorf(
					"%s: missing declaration — every (invariant, vertical) pair must be "+
						"declared", Key{Invariant: i, Vertical: v}))
			}
		}
	}

	seen := make(map[string]bool, len(exceptions))
	for _, e := range exceptions {
		switch {
		case e.Source == "":
			errs = append(errs, errors.New("source exception: empty source value"))
			continue
		case seen[e.Source]:
			errs = append(errs, fmt.Errorf("source exception %q: declared twice", e.Source))
		}
		seen[e.Source] = true
		if e.Reason == "" {
			errs = append(errs, fmt.Errorf("source exception %q: reason must not be empty", e.Source))
		}
		if e.Quote == "" {
			errs = append(errs, fmt.Errorf("source exception %q: quote must not be empty", e.Source))
		}
		if e.Evidence == "" {
			errs = append(errs, fmt.Errorf("source exception %q: evidence must not be empty", e.Source))
		}
	}

	return errs
}
