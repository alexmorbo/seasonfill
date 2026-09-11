package verticals

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRegistryIsWellFormed is the guard on the declaration itself:
// completeness, mandatory reasons on Deferred, mandatory closing phase on
// Gap, evidence everywhere.
func TestRegistryIsWellFormed(t *testing.T) {
	t.Parallel()
	errs := Validate()
	for _, e := range errs {
		t.Errorf("registry defect: %v", e)
	}
	require.Empty(t, errs)
}

// TestRegistryMatchesADRTable pins the declaration to the ADR-0025 R4
// table cell by cell. Changing a cell without touching this table (and
// therefore without re-reading the ADR) is not possible.
func TestRegistryMatchesADRTable(t *testing.T) {
	t.Parallel()

	want := map[Key]State{
		{InvariantFailureJournal, VerticalSeries}: StateHeld,
		// Closed by ADR-0025 Ф1a: MovieWorker.HandleForced now journals every
		// /movie/{id} failure through EnrichmentErrors.RecordFailure.
		{InvariantFailureJournal, VerticalMovie}: StateHeld,
		{InvariantPickerBreaker, VerticalSeries}: StateHeld,
		// Closed by ADR-0025 Ф1b: both movie picker tier arms carry the
		// attempts>5 terminal-failure gate.
		{InvariantPickerBreaker, VerticalMovie}: StateHeld,
		{InvariantRetrySweep, VerticalSeries}:   StateHeld,
		// Closed by ADR-0025 Ф1b: runNightlyTick sweeps SourceTMDBMovie retries
		// into the EntityMovie lane.
		{InvariantRetrySweep, VerticalMovie}:         StateHeld,
		{InvariantLoopDeclaresTypes, VerticalSeries}: StateHeld,
		// Closed by ADR-0025 Ф2: regrab declares SupportedInstanceTypes and
		// skips instances of an unsupported arr type instead of failing on
		// them every 30 minutes.
		{InvariantLoopDeclaresTypes, VerticalMovie}: StateHeld,
		{InvariantRegrabSupported, VerticalSeries}:  StateHeld,
		{InvariantRegrabSupported, VerticalMovie}:   StateDeferred,
		{InvariantMetricsNamespace, VerticalSeries}: StateHeld,
		{InvariantMetricsNamespace, VerticalMovie}:  StateGap,
		{InvariantDomainLogger, VerticalSeries}:     StateHeld,
		{InvariantDomainLogger, VerticalMovie}:      StateHeld,
	}

	require.Len(t, registry, len(want))
	for k, wantState := range want {
		got, ok := Lookup(k.Invariant, k.Vertical)
		require.Truef(t, ok, "%s: missing from the registry", k)
		require.Equalf(t, wantState, got.State, "%s: state drifted from the ADR R4 table", k)
	}
}

// TestDeferredCellsCarryReasonAndReference is the R2-bis rule spelled out
// on the real data: a Deferred cell must say why and where it is tracked.
func TestDeferredCellsCarryReasonAndReference(t *testing.T) {
	t.Parallel()
	deferred := 0
	for _, e := range All() {
		if e.Status.State != StateDeferred {
			continue
		}
		deferred++
		require.NotEmptyf(t, e.Status.Reason, "%s: deferred without a reason", e.Key)
		require.Containsf(t, e.Status.Reason, "B-41",
			"%s: deferred reason must name the backlog item that tracks it", e.Key)
	}
	require.Equal(t, 1, deferred, "exactly one deferred cell today: regrab_supported/movie")
}

// TestMetricsNamespaceMovieDeclaresItsSubject locks in the honesty fix:
// the movie-column metrics gap belongs to the search bounded context, not
// to the movie vertical, and the declaration must say so.
func TestMetricsNamespaceMovieDeclaresItsSubject(t *testing.T) {
	t.Parallel()
	s := MustLookup(InvariantMetricsNamespace, VerticalMovie)
	require.Equal(t, StateGap, s.State)
	require.Contains(t, s.Subject, "search")
}

func TestAllIsSortedAndComplete(t *testing.T) {
	t.Parallel()
	all := All()
	require.Len(t, all, len(AllInvariants)*len(AllVerticals))
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1].Key, all[i].Key
		if prev.Invariant == cur.Invariant {
			require.Less(t, string(prev.Vertical), string(cur.Vertical), "All() must be sorted")
			continue
		}
		require.Less(t, string(prev.Invariant), string(cur.Invariant), "All() must be sorted")
	}
}

func TestLookupMissesUnknownCell(t *testing.T) {
	t.Parallel()
	_, ok := Lookup(Invariant("no_such_invariant"), VerticalSeries)
	require.False(t, ok)
}

func TestMustLookupPanicsOnUnknownCell(t *testing.T) {
	t.Parallel()
	require.Panics(t, func() { MustLookup(Invariant("no_such_invariant"), VerticalMovie) })
}

func TestStateIsValid(t *testing.T) {
	t.Parallel()
	for _, s := range []State{StateHeld, StateGap, StateDeferred} {
		require.Truef(t, s.IsValid(), "%q must be valid", s)
	}
	for _, s := range []State{"", "unknown", "HELD"} {
		require.Falsef(t, s.IsValid(), "%q must be invalid", s)
	}
}

func TestKeyString(t *testing.T) {
	t.Parallel()
	require.Equal(t, "picker_breaker/movie",
		Key{Invariant: InvariantPickerBreaker, Vertical: VerticalMovie}.String())
}

func TestJournalledSourceExceptions(t *testing.T) {
	t.Parallel()
	require.True(t, IsJournalledSourceException("sonarr"))
	require.True(t, IsJournalledSourceException("qbit"))
	require.False(t, IsJournalledSourceException("tmdb_series"))
	require.False(t, IsJournalledSourceException(""))
	for _, e := range JournalledSourceExceptions {
		require.NotEmpty(t, e.Reason)
		require.NotEmpty(t, e.Quote)
		require.NotEmpty(t, e.Evidence)
	}
}

// --- validator failure branches, driven with synthetic tables ---------

// fullTable returns a minimal well-formed cross product so each negative
// case below can break exactly one thing.
func fullTable() map[Key]Status {
	out := make(map[Key]Status, len(AllInvariants)*len(AllVerticals))
	for _, i := range AllInvariants {
		for _, v := range AllVerticals {
			out[Key{Invariant: i, Vertical: v}] = Status{State: StateHeld, Evidence: "x.go:1"}
		}
	}
	return out
}

func okExceptions() []SourceException {
	return []SourceException{{Source: "sonarr", Reason: "r", Quote: "q", Evidence: "e"}}
}

func TestValidateAcceptsWellFormedTable(t *testing.T) {
	t.Parallel()
	require.Empty(t, validate(fullTable(), okExceptions()))
}

func TestValidateRejectsDeferredWithoutReason(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	k := Key{Invariant: InvariantRegrabSupported, Vertical: VerticalMovie}
	tbl[k] = Status{State: StateDeferred, Evidence: "x.go:1"}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "deferred without a reason")
}

func TestValidateRejectsDeferredWithClosingPhase(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	k := Key{Invariant: InvariantRegrabSupported, Vertical: VerticalMovie}
	tbl[k] = Status{State: StateDeferred, Reason: "r", Evidence: "x.go:1", ClosedBy: "F9"}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "must not name a closing phase")
}

func TestValidateRejectsGapWithoutClosingPhase(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	k := Key{Invariant: InvariantPickerBreaker, Vertical: VerticalMovie}
	tbl[k] = Status{State: StateGap, Evidence: "x.go:1"}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "gap without a closing phase")
}

func TestValidateRejectsHeldWithClosingPhase(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	k := Key{Invariant: InvariantDomainLogger, Vertical: VerticalSeries}
	tbl[k] = Status{State: StateHeld, Evidence: "x.go:1", ClosedBy: "F1"}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "held cells must not name a closing phase")
}

func TestValidateRejectsMissingEvidence(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	k := Key{Invariant: InvariantRetrySweep, Vertical: VerticalSeries}
	tbl[k] = Status{State: StateHeld}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "evidence must not be empty")
}

func TestValidateRejectsIncompleteTable(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	delete(tbl, Key{Invariant: InvariantFailureJournal, Vertical: VerticalMovie})
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "missing declaration")
}

func TestValidateRejectsUnknownStateAndKeys(t *testing.T) {
	t.Parallel()
	tbl := fullTable()
	tbl[Key{Invariant: "bogus", Vertical: "nowhere"}] = Status{State: "maybe", Evidence: "x.go:1"}
	errs := validate(tbl, okExceptions())
	require.Len(t, errs, 3)
	joined := errs[0].Error() + "|" + errs[1].Error() + "|" + errs[2].Error()
	require.Contains(t, joined, "unknown invariant")
	require.Contains(t, joined, "unknown vertical")
	require.Contains(t, joined, "unknown state")
}

func TestValidateRejectsBadExceptions(t *testing.T) {
	t.Parallel()
	cases := map[string][]SourceException{
		"empty source value":         {{Source: "", Reason: "r", Quote: "q", Evidence: "e"}},
		"reason must not be empty":   {{Source: "sonarr", Quote: "q", Evidence: "e"}},
		"quote must not be empty":    {{Source: "sonarr", Reason: "r", Evidence: "e"}},
		"evidence must not be empty": {{Source: "sonarr", Reason: "r", Quote: "q"}},
		"source exception \"qbit\": declared twice": {
			{Source: "qbit", Reason: "r", Quote: "q", Evidence: "e"},
			{Source: "qbit", Reason: "r", Quote: "q", Evidence: "e"},
		},
	}
	for want, exc := range cases {
		t.Run(want, func(t *testing.T) {
			errs := validate(fullTable(), exc)
			require.NotEmpty(t, errs)
			msgs := make([]string, 0, len(errs))
			for _, e := range errs {
				msgs = append(msgs, e.Error())
			}
			require.Contains(t, strings.Join(msgs, "|"), want)
		})
	}
}
