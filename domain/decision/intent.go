package decision

// ChosenBecause is the structured "why this candidate won" enum
// persisted on every Decision row's Intent payload. Plain strings on
// the wire — the SPA GrabDrawer renders a localised label per value;
// adding a new value here is an additive change (frontend falls back
// to the raw string).
type ChosenBecause string

const (
	// ChosenBecauseOnlyCandidate — single candidate survived filtering.
	// No comparison was performed; we picked the only one available.
	ChosenBecauseOnlyCandidate ChosenBecause = "only_candidate"
	// ChosenBecauseHighestScore — beat alternatives on
	// CustomFormatScore (Sonarr's own ranking signal). The default
	// for any multi-candidate scan path; `ChosenReasonDetail` carries
	// the score comparison (e.g. "score 88 vs alternates 64, 71").
	ChosenBecauseHighestScore ChosenBecause = "highest_score"
	// ChosenBecauseFirstPassQuality — first available release met the
	// wanted resolution. Reserved for future quality-gated picks where
	// the score path doesn't apply.
	ChosenBecauseFirstPassQuality ChosenBecause = "first_pass_quality"
	// ChosenBecauseWatchdogBetterQuality — replay path (Phase 10
	// Watchdog re-grab). Selected release improves on the incumbent's
	// resolution / codec / HDR axis.
	ChosenBecauseWatchdogBetterQuality ChosenBecause = "watchdog_better_quality"
	// ChosenBecauseWatchdogBetterDub — replay path. Selected release
	// gained a dub track the incumbent did not have.
	ChosenBecauseWatchdogBetterDub ChosenBecause = "watchdog_better_dub"
	// ChosenBecauseWatchdogBetterOther — replay path with an axis we
	// can't classify (parent off-page, parent unparsed, no comparable
	// data). The row IS a replay but the improvement reason is
	// implicit.
	ChosenBecauseWatchdogBetterOther ChosenBecause = "watchdog_better_other"
	// ChosenBecauseManualSelection — operator-initiated pick via the
	// manual-mode handler. Bypasses scoring entirely.
	ChosenBecauseManualSelection ChosenBecause = "manual_selection"
)

// IsValid reports whether the value is one of the known enum strings.
// Unknown values are still persisted (so future frontend code can
// recognise newer agents' values) but `IsValid` is the constructor's
// gate against typos in current callers.
func (c ChosenBecause) IsValid() bool {
	switch c {
	case ChosenBecauseOnlyCandidate,
		ChosenBecauseHighestScore,
		ChosenBecauseFirstPassQuality,
		ChosenBecauseWatchdogBetterQuality,
		ChosenBecauseWatchdogBetterDub,
		ChosenBecauseWatchdogBetterOther,
		ChosenBecauseManualSelection:
		return true
	}
	return false
}

// Intent is the per-Decision "why this grab" capture. Persisted as a
// single JSON column (`decisions.intent`) so future axis additions
// don't cost a schema migration. All four fields are optional on the
// wire: pre-091a rows carry no intent and the DTO emits `null`;
// best-effort fields that the call site can't populate (e.g. unknown
// target_episodes on the all_complete pre-filter path) stay nil/empty.
//
// TargetEpisodes is the set of missing-aired episode numbers that
// triggered the scan path. Empty slice = no episode-level data was
// available (synthetic pre-filter rows).
//
// HadEpisodes is the set of episode numbers we already had on disk at
// scan time (Sonarr-side `has_file=true`). Mirrors TargetEpisodes'
// semantics: empty when the call site lacks per-episode data.
//
// ChosenBecause is one of the structured enum values above. Stored as
// a plain string so the wire shape stays stable across enum
// extensions.
//
// ChosenReasonDetail is free-text amplification of ChosenBecause.
// Typical contents: "score 88 vs 64", "+RUS DD5.1", "1080p Web-DL
// beats 720p HDTV". No structured parsing — the SPA renders it
// verbatim as the secondary line under the chosen_because chip.
type Intent struct {
	TargetEpisodes     []int         `json:"target_episodes"`
	HadEpisodes        []int         `json:"had_episodes"`
	ChosenBecause      ChosenBecause `json:"chosen_because"`
	ChosenReasonDetail string        `json:"chosen_reason_detail"`
}

// NewIntent constructs an Intent value. Defensive copies the input
// slices so the caller can mutate its working buffers without
// touching the persisted payload. Empty / nil slices are stored as
// `[]int{}` so the JSON round-trip stays stable (an `omitempty`-style
// distinction between nil and empty is not required at the DTO layer
// — both render as `[]` on the wire, which the frontend handles).
//
// `because` is NOT validated against IsValid here on purpose: callers
// that compose a chosen_because from runtime data (e.g. the replay
// path that maps a domain/grab.ReplayKind onto an enum value) would
// otherwise fail-noisy on an unmapped kind. Validation is the
// caller's responsibility; future agents can extend ChosenBecause
// without rewriting every call site.
func NewIntent(target, had []int, because ChosenBecause, detail string) Intent {
	t := make([]int, len(target))
	copy(t, target)
	h := make([]int, len(had))
	copy(h, had)
	return Intent{
		TargetEpisodes:     t,
		HadEpisodes:        h,
		ChosenBecause:      because,
		ChosenReasonDetail: detail,
	}
}
