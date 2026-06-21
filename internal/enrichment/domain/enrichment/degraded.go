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
// one entity-page render. The shape is column-on-canon + per-source
// errors: SyncedAt maps Source → last-success timestamp (read from
// canon enrichment_*_synced_at columns); Errors maps Source → current
// outstanding enrichment_errors row (nil = no live error / cleared
// on success).
type DegradedInput struct {
	// SyncedAt maps Source → last successful enrichment timestamp
	// (nil = never enriched). Reads canon series.enrichment_*_synced_at
	// columns directly (and people.enrichment_synced_at for the
	// tmdb_person source).
	SyncedAt map[Source]*time.Time
	// Errors maps Source → current outstanding error row (nil = no
	// live error / cleared on success). Reads enrichment_errors rows
	// filtered by IsLive window.
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
// v4 §5.6. Rules:
//
//  1. Source has no sync record → degraded.
//  2. Source has a live error row → degraded.
//  3. Source's last success is older than now − 2×TTL → degraded.
//  4. SonarrReachable=false → SourceSonarr appended.
//  5. QbitReachable=false → SourceQbit appended.
//
// A source is "relevant" only if the caller declared it via the
// SyncedAt or Errors map; sources declared in neither are silently
// skipped (caller did not ask about them).
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
			if !sourceRelevant(in, src) {
				continue
			}
			degraded = sourceDegraded(in, src, now)
		}
		if degraded && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	return out
}

// sourceRelevant reports whether the caller declared `src` as
// relevant by including it in either of the input maps.
func sourceRelevant(in DegradedInput, src Source) bool {
	if _, ok := in.SyncedAt[src]; ok {
		return true
	}
	if _, ok := in.Errors[src]; ok {
		return true
	}
	return false
}

// sourceDegraded applies rules 1-3 to one source.
func sourceDegraded(in DegradedInput, src Source, now time.Time) bool {
	syncedAt := in.SyncedAt[src]
	errEntry := in.Errors[src]
	// Rule 1 — never enriched.
	if syncedAt == nil {
		return true
	}
	// Rule 2 — live error row.
	if errEntry != nil {
		return true
	}
	// Rule 3 — staleness.
	ttl := in.TTLs[src]
	return IsStale(syncedAt, ttl, now)
}
