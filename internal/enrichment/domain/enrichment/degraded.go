package enrichment

import "time"

// SourceSonarr / SourceQbit — extension of the Source enum in
// sync_log.go for the live-source reachability flags. These
// values do NOT have sync_log rows (the live sources are queried
// per-request, not journalled); they appear in degraded[] only
// via the reachability flag path.
const (
	SourceSonarr Source = "sonarr"
	SourceQbit   Source = "qbit"
)

// DegradedInput carries everything the Degraded calculator
// needs for one entity-page render: the per-source latest
// sync_log row (nil = "never synced"), per-source TTL value
// (looked up by the caller via TTL(source, kind)), and the two
// live-source reachability flags. Map shape lets callers pass a
// dense subset — Degraded only checks the sources present in
// `Logs`, plus the two reachability flags. Sources not in the
// map AND not in the reachability flags are silently dropped
// (not "never synced" — caller is responsible for declaring
// which sources are relevant for the entity).
type DegradedInput struct {
	// Logs maps Source → latest SyncLog row (nil pointer
	// allowed: "this source has been queried for sync_log but
	// no row exists — never synced"). The caller's
	// repository.LatestBySources(entity_type, entity_id, []Source)
	// produces this map.
	Logs map[Source]*SyncLog
	// TTLs maps Source → TTL value (caller pre-computed via
	// TTL(source, kind)). Sources without a TTL entry are
	// checked against rules 1 and 2 (no row / error) but skip
	// rule 3 (staleness — no cutoff to compare against).
	TTLs map[Source]time.Duration
	// SonarrReachable is the live Sonarr instance health flag.
	// False → degraded[] includes SourceSonarr.
	SonarrReachable bool
	// QbitReachable is the live qBit instance health flag.
	// False → degraded[] includes SourceQbit.
	QbitReachable bool
}

// IsStale reports whether a sync_log entry has crossed the
// "really stale" threshold (PRD §5.6 rule 3): synced_at is
// older than `now − 2×TTL`. A nil SyncedAt (never synced) is
// NOT stale by this rule — the "never synced" condition is
// rule 1, handled separately by Degraded.
//
// ttl=0 disables rule 3 (live source) — IsStale returns false.
func IsStale(entry SyncLog, ttl time.Duration, now time.Time) bool {
	if ttl == 0 || entry.SyncedAt == nil {
		return false
	}
	cutoff := now.Add(-2 * ttl)
	return entry.SyncedAt.Before(cutoff)
}

// Degraded computes the degraded-sources list for one entity
// per PRD v4 §5.6. Rules:
//
//  1. Source has no sync_log row → degraded.
//  2. Latest sync_log row has outcome=error → degraded.
//  3. Latest sync_log row is older than now − 2×TTL → degraded.
//  4. SonarrReachable=false → SourceSonarr appended.
//  5. QbitReachable=false → SourceQbit appended.
//
// Output ordering is deterministic (UI surface stability):
// TMDB series, TMDB season, TMDB person, OMDb, Sonarr, qBit.
// Each source appears at most once (dedup on the way out).
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
			entry, present := in.Logs[src]
			switch {
			case !present:
				// Source not in input map → caller did not
				// declare it relevant for this entity. Skip.
				continue
			case entry == nil:
				// Rule 1: never synced.
				degraded = true
			case entry.Outcome == OutcomeError:
				// Rule 2: latest attempt failed.
				degraded = true
			default:
				// Rule 3: stale.
				ttl := in.TTLs[src]
				degraded = IsStale(*entry, ttl, now)
			}
		}
		if degraded && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	return out
}
