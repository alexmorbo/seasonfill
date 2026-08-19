package domain

// FreshenResult reports what the synchronous read-through Freshener did for one
// media detail open. It UNIFIES the two pre-existing copies
// (internal/seriesdetail/app.FreshenResult and
// internal/moviedetail/app.FreshenResult) — those two are left untouched by S1;
// the engine's own callers use this type. Exactly one flag is the headline:
//   - Fresh: nothing needed refreshing (or nothing was registered to check).
//   - Refreshed: the stale sections were refreshed within the sync budget; the
//     caller MUST re-read the canon to observe the hydrated rows.
//   - Degraded: refresh timed out / errored, or the engine was not yet bound;
//     the caller falls back to the async path and surfaces a degraded marker.
type FreshenResult struct {
	Refreshed bool
	Fresh     bool
	Degraded  bool
}
