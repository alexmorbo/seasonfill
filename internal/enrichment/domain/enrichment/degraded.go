package enrichment

import "time"

// SourceSonarr / SourceQbit — extension of the Source enum in
// domain.go for the live-source reachability flags. These
// values do NOT have enrichment_errors rows (the live sources are
// queried per-request, not journalled); they appear in degraded[]
// only via the reachability flag path.
const (
	SourceSonarr Source = "sonarr"
	SourceQbit   Source = "qbit"
)

// DegradedInput carries everything the Degraded calculator needs for
// one entity-page render. The shape carries both the legacy `Logs`
// map (preserved during 464a for composer compile-green) AND the new
// `SyncedAt` + `Errors` maps that 464b's composer rewrite will
// populate exclusively. The Degraded() body branches on which fields
// the caller filled — both shapes coexist until 464b drops `Logs`.
type DegradedInput struct {
	// Logs maps Source → latest SyncLog row (nil pointer allowed:
	// "this source has been queried for sync_log but no row exists
	// — never synced").
	//
	// NOTE (464a → 464b): 464b will delete this field together with
	// the SyncLog struct. The 464a composer keeps writing it because
	// the SyncLogStub panics at request time — no production composer
	// call actually populates Logs after 464a deploys (the stub
	// raises before the map write), but the dead branch keeps the
	// composer compile-green.
	Logs map[Source]*SyncLog
	// SyncedAt maps Source → last successful enrichment timestamp
	// (nil = never enriched). Reads canon series.enrichment_*_synced_at
	// columns directly. 464a composer rewrite leaves this nil — only
	// 464b populates it.
	SyncedAt map[Source]*time.Time
	// Errors maps Source → current outstanding error row (nil = no
	// live error / cleared on success). Reads enrichment_errors rows
	// filtered by IsLive window. 464a composer rewrite leaves this
	// nil — only 464b populates it.
	Errors map[Source]*EnrichmentError
	// TTLs maps Source → TTL value (caller pre-computed via
	// TTL(source, kind)). Sources without a TTL entry are checked
	// against rules 1 and 2 (no row / error) but skip rule 3
	// (staleness — no cutoff to compare against).
	TTLs map[Source]time.Duration
	// SonarrReachable is the live Sonarr instance health flag.
	// False → degraded[] includes SourceSonarr.
	SonarrReachable bool
	// QbitReachable is the live qBit instance health flag.
	// False → degraded[] includes SourceQbit.
	QbitReachable bool
}

// IsStale reports whether a syncedAt timestamp has crossed the "really
// stale" threshold (PRD §5.6 rule 3): older than `now − 2×TTL`. A nil
// syncedAt (never synced) is NOT stale by this rule — the "never
// synced" condition is rule 1, handled separately by Degraded.
//
// ttl=0 disables rule 3 (live source) — IsStale returns false.
func IsStale(syncedAt *time.Time, ttl time.Duration, now time.Time) bool {
	if ttl == 0 || syncedAt == nil {
		return false
	}
	cutoff := now.Add(-2 * ttl)
	return syncedAt.Before(cutoff)
}

// Degraded computes the degraded-sources list for one entity per PRD
// v4 §5.6. Rules (same as pre-464a, only the input shape changed):
//
//  1. Source has no sync record → degraded.
//  2. Source has a live error row → degraded.
//  3. Source's last success is older than now − 2×TTL → degraded.
//  4. SonarrReachable=false → SourceSonarr appended.
//  5. QbitReachable=false → SourceQbit appended.
//
// The calculator reads from `SyncedAt`+`Errors` first (the 464b
// canonical shape); on empty input it falls back to the legacy `Logs`
// map for compile-green coverage of the 464a composer path. A source
// declared in EITHER shape is "relevant"; sources declared in neither
// are silently skipped (caller did not ask about them).
//
// Output ordering is deterministic (UI surface stability): TMDB
// series, TMDB season, TMDB person, OMDb, Sonarr, qBit. Each source
// appears at most once (dedup on the way out).
//
// `now` is injected — the function does NOT call time.Now().
func Degraded(in DegradedInput, now time.Time) []Source {
	canonicalOrder := []Source{
		SourceTMDBSeries, SourceTMDBSeason,
		SourceTMDBPerson, SourceOMDb,
		SourceSonarr, SourceQbit,
	}
	seen := make(map[Source]bool, len(canonicalOrder))
	out := make([]Source, 0, len(canonicalOrder))
	for _, src := range canonicalOrder {
		degraded := false
		switch src {
		case SourceSonarr:
			degraded = !in.SonarrReachable
		case SourceQbit:
			degraded = !in.QbitReachable
		default:
			degraded = sourceDegraded(in, src, now)
			if !sourceRelevant(in, src) {
				continue
			}
		}
		if degraded && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	return out
}

// sourceRelevant reports whether the caller declared `src` as
// relevant by including it in any of the input maps.
func sourceRelevant(in DegradedInput, src Source) bool {
	if _, ok := in.SyncedAt[src]; ok {
		return true
	}
	if _, ok := in.Errors[src]; ok {
		return true
	}
	if _, ok := in.Logs[src]; ok {
		return true
	}
	return false
}

// sourceDegraded applies rules 1-3 to one source. New-shape inputs
// (SyncedAt/Errors) win over the legacy Logs map when both are
// populated — 464a composer writes Logs only, so the legacy branch
// activates; 464b's composer writes SyncedAt/Errors so this branch
// activates.
func sourceDegraded(in DegradedInput, src Source, now time.Time) bool {
	syncedAt, syncedAtPresent := in.SyncedAt[src]
	errEntry, errPresent := in.Errors[src]
	if syncedAtPresent || errPresent {
		// New shape: rule 1 (no sync), rule 2 (live error), rule 3 (stale).
		if syncedAt == nil {
			return true
		}
		if errEntry != nil {
			return true
		}
		ttl := in.TTLs[src]
		return IsStale(syncedAt, ttl, now)
	}
	// Legacy Logs path — preserved for 464a composer compile-green.
	entry, present := in.Logs[src]
	if !present {
		return false
	}
	if entry == nil {
		return true // rule 1
	}
	if entry.Outcome == OutcomeError {
		return true // rule 2
	}
	ttl := in.TTLs[src]
	return isStaleSyncLog(*entry, ttl, now) // rule 3
}

// isStaleSyncLog mirrors the pre-464a IsStale on a SyncLog struct.
// Kept private as a transitional helper while the legacy Logs path
// stays compile-callable; deleted in 464b.
func isStaleSyncLog(entry SyncLog, ttl time.Duration, now time.Time) bool {
	return IsStale(entry.SyncedAt, ttl, now)
}
