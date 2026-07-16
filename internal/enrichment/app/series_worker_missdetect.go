package enrichment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// ChangesMissMetric is the narrow observability seam the miss-detector writes to.
// Production impl: *observability.TMDBChangesMetrics (IncMiss →
// seasonfill_tmdb_changes_miss_total). Kept SEPARATE from the poller's
// ChangesMetrics port (changes_poller.go) so the poller's recording fake does not
// have to grow an IncMiss method, and so the concrete adapter can satisfy both.
// nil-OK on SeriesWorkerDeps — when nil the miss-detector no-ops.
type ChangesMissMetric interface {
	IncMiss()
}

// recordChangesMissIfDetected is the W2-8 (G3 / ADR-0002) firehose-recall probe.
// It is OBSERVABILITY-ONLY: it writes nothing, returns nothing, mutates nothing
// except the seasonfill_tmdb_changes_miss_total counter. It must NEVER let a
// cursor read error or any other condition affect the refresh — every failure
// path returns silently (best-effort).
//
// It counts a "miss" when the TMDB /tv/changes firehose demonstrably failed to
// flag a real change we then observed on a normal refresh. ALL must hold:
//
//	(a) before.EnrichmentTMDBSyncedAt != nil          — prior sync existed
//	(b) a whitelisted canon field actually differs     — real content change
//	(c) before.TMDBChangedAt is NULL, or <= synced_at   — firehose did not flag ahead
//	(d) cursor.LastWindowEnd >= date(synced_at)+24h      — firehose covered the interval (M-03)
//
// M-01 corollary: the detector measures recall ONLY over canon-representable
// fields (M-02 whitelist below). Text/cast/media-key changes are NOT covered; a
// near-zero same-day reading means same-day self-healing (next-day re-mark),
// NOT that the miss class is absent.
//
// M-02 whitelist = Status, FirstAirDate, LastAirDate, NextAirDate, RuntimeMinutes,
// OriginalTitle. Popularity / TMDBRating (vote_average) / TMDBVotes (vote_count)
// are HARD-EXCLUDED — aggregate rating drift would make miss≈refresh and kill the
// gate (contradicts G5, Wave 2 = content only). number_of_seasons /
// number_of_episodes are NOT on canon, so they cannot diff without a schema change
// (out of scope); new-season / new-episode changes surface INDIRECTLY through the
// LastAirDate / NextAirDate deltas.
//
// M-03 (guard d) also makes the detector naturally inert while the poller is
// dark-launched OFF: the cursor never advances, so LastWindowEnd stays behind the
// threshold and no miss is ever counted.
func (w *SeriesWorker) recordChangesMissIfDetected(ctx context.Context, before series.Canon, tv *tmdb.TVResponse, log *slog.Logger) {
	// nil-OK deps: either seam unset ⇒ detector fully disabled.
	if w.deps.ChangesMiss == nil || w.deps.ChangesCursor == nil {
		return
	}
	// (a) prior sync must exist — on-demand first population is not a miss.
	if before.EnrichmentTMDBSyncedAt == nil {
		return
	}
	// (b) at least one whitelisted canon field must actually differ. after is a
	//     pure in-memory map of the already-fetched tv payload — zero queries.
	after := tmdb.MapTVToCanon(tv)
	if !changesWhitelistCanonDiff(before, after) {
		return
	}
	// (c) the firehose must NOT have flagged this change ahead of the sync.
	//     tmdb_changed_at > synced_at ⇒ correctly flagged ⇒ not a miss.
	if before.TMDBChangedAt != nil && before.TMDBChangedAt.After(*before.EnrichmentTMDBSyncedAt) {
		return
	}
	// (d) M-03 coverage guard — count only when the firehose window covered the
	//     interval since the last sync. Empty cursor (ErrNotFound / zero
	//     LastWindowEnd) ⇒ not covered ⇒ not a miss (dark-launch-inert).
	cursor, err := w.deps.ChangesCursor.Get(ctx)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			log.DebugContext(ctx, "enrichment.changes.miss.cursor_read_failed",
				slog.String("error", err.Error()))
		}
		return
	}
	if cursor.LastWindowEnd.Before(utcDatePlusDay(*before.EnrichmentTMDBSyncedAt)) {
		return
	}

	w.deps.ChangesMiss.IncMiss()
	log.DebugContext(ctx, "enrichment.changes.miss.detected",
		slog.Int64("series_id", int64(before.ID)),
		slog.Time("synced_at", *before.EnrichmentTMDBSyncedAt),
	)
}

// changesWhitelistCanonDiff reports whether before and after differ on any M-02
// whitelisted canon field. Popularity / TMDBRating / TMDBVotes are intentionally
// NOT compared (aggregate drift is excluded — see recordChangesMissIfDetected).
func changesWhitelistCanonDiff(before, after series.Canon) bool {
	return !missStrEqual(before.Status, after.Status) ||
		!missTimeEqual(before.FirstAirDate, after.FirstAirDate) ||
		!missTimeEqual(before.LastAirDate, after.LastAirDate) ||
		!missTimeEqual(before.NextAirDate, after.NextAirDate) ||
		!missIntEqual(before.RuntimeMinutes, after.RuntimeMinutes) ||
		!missStrEqual(before.OriginalTitle, after.OriginalTitle)
}

// utcDatePlusDay returns UTC-midnight of t plus 24h — the M-03 coverage threshold
// (the firehose must have processed at least the calendar day AFTER the sync).
func utcDatePlusDay(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// missStrEqual / missIntEqual / missTimeEqual: dereferenced-value equality with
// nil-safe semantics (both nil → equal; one nil → differ; both set → compare).
func missStrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func missIntEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func missTimeEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
