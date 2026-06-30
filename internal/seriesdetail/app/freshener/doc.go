// Package freshener answers "which sections of a series detail are
// stale for this lang?" via the Probe interface, and provides the
// per-section TTL policy table (Sonarr-inspired floor/ceiling/status-
// aware state machine) backing the answer.
//
// Section taxonomy: 5 DENSE fixed sections (skeleton, overview, cast,
// recommendations, media) + N SPARSE per-season verdicts (one per
// element in seasonNumbers []int passed by the caller). Order is
// stable: FixedSections declaration order, then season verdicts in
// input order. F-R2-2 signature: Probe.IsStale(ctx, seriesID, lang,
// seasonNumbers).
//
// Probe is read-only and pure (no enqueue, no force bool). The
// SeriesFreshener.EnsureFreshScope driver (A5) maps verdicts to narrow
// Worker methods (A2-A4 RefreshSeriesText/RefreshCast/RefreshSeasonSlim/
// RefreshRecommendations/RefreshMediaAssets, all of which carry their
// own force bool parameter — Probe never has one).
//
// Fail-open per Radarr lesson: every Probe IO error path returns a
// stale verdict with reason "probe_error", never refuses to decide.
// Only ctx.Err surfaces to the caller — composer abandons properly.
//
// State-machine TTL: see ttl.go. Per-section Floor + Ceiling +
// StatusAware. Returning Series / In Production refresh at Floor on
// status-aware sections; Ended/Canceled wait for Ceiling.
//
// See documentation/refactor-first/PLAN-2026-07-01.md §6.1 for the
// design narrative.
package freshener
