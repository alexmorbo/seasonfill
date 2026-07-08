package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RefreshCandidate is one row of the Story 534 tiered picker. Tier
// labels the source bucket (hot/normal/cold); SyncedAt is nullable
// (NULL = never enriched, sorted first within the tier). MissingPoster
// is true ONLY when the row qualified EXCLUSIVELY via the HOT poster
// guard: it lacks a series_media_texts.poster_asset AND its normal
// TTL/staleness predicate was NOT already satisfied (TTL-fresh, non-NULL
// sync). A poster-less row that would also be picked by the normal HOT
// staleness gate (TTL expired or NULL sync) is a NORMAL pick and carries
// MissingPoster=false. Always false for NORMAL/COLD (poster branch is
// HOT-only).
type RefreshCandidate struct {
	SeriesID      domain.SeriesID
	Tier          enrichment.RefreshTier
	SyncedAt      *time.Time
	MissingPoster bool
}

// PickRefreshCandidates returns up to `limit` candidates across all
// three tiers, ordered by priority (hot → cold) and within-tier by
// staleness ascending (NULL first, then oldest first).
//
// Tier semantics:
//   - HOT: EXISTS in series_cache (deleted_at IS NULL).
//   - NORMAL: EXISTS in discovery_lists AND NOT in HOT.
//   - COLD: tmdb_id IS NOT NULL AND NOT in HOT AND NOT in NORMAL.
//
// All three tiers require:
//   - series.tmdb_id IS NOT NULL (TMDB-enrichable),
//   - enrichment_tmdb_synced_at IS NULL OR < now - ttl(tier),
//   - NOT EXISTS enrichment_errors row with attempts > 5 for this
//     entity/source (terminal-failure exclude — matches ListStaleForTMDB).
//
// W17-1 — the HOT branch additionally selects a library series whose
// TMDB sync is otherwise fresh but which has NO series_media_texts row
// with poster_asset IS NOT NULL. The missing_poster CASE is attributed
// EXCLUSIVELY to these poster-guard-only picks: it repeats the hotCutoff
// comparison so a row already stale enough to be a normal HOT pick (or
// NULL-sync) is NOT double-counted as a poster pick. This heals the 49
// library series that
// were enriched by a cold-start sweep predating the poster-seed code
// (they are tmdb-stamped so the TTL gate skips them) and acts as a
// permanent guard against any future poster-less series. A 15-minute
// race guard (enrichment_tmdb_synced_at < now-15m) keeps a series that
// is mid-Handle from being yanked into a concurrent refresh. The guard
// lives INSIDE the HOT branch, so it inherits tmdb_id IS NOT NULL and
// the series_cache scope — tmdb-less Sonarr stubs are never selected.
//
// #1090b — the HOT branch ALSO selects a library series that has
// media_type='tv' person_credits rows but NONE carries a non-NULL
// last_appearance_season, so the #1090 aggregate_credits backfill lands
// promptly instead of waiting up to a week for the 7d TTL. Two bounds
// keep a genuinely-unfillable series (crew-only / specials-only cast,
// whose tv rows stay NULL forever) from re-picking every tick:
//  1. EXISTS a media_type='tv' row (tmdb_media_id = s.tmdb_id) — a
//     no-cast / TMDB-fallback series has zero tv rows and never enters
//     the branch;
//  2. a dedicated 6-hour cooldown
//     (enrichment_tmdb_synced_at < now-6h). HandleForced stamps
//     enrichment_tmdb_synced_at=now, so a just-healed-still-NULL series
//     is ineligible for 6h → worst-case re-pick once/6h, not once/tick.
//
// A fillable series self-clears after one heal (≥1 tv row gains a
// non-NULL value → the NOT EXISTS fails). Heal picks ride as normal HOT
// picks (tier=1); the scheduler runs HandleForced's full all-seasons
// aggregate_credits for every HOT pick, so no new signal field is needed.
//
// The query is one UNION ALL'd round-trip so the LIMIT applies across
// the priority-ordered union, NOT per-tier — which is the desired
// budget behaviour ("drain hot first, then normal, then cold").
//
// Portable across Postgres + SQLite: literal '1970-01-01' for NULL
// sentinel ordering, literal 'tv' discriminator, EXISTS/NOT EXISTS only
// (no array_agg, no JSON aggregation, no dialect casts). The terminal-
// failure guard is the same NOT EXISTS shape `ListStaleForTMDB` ships
// today (see series_repository.go:391-401) and travels through the
// same `enrichment_errors_pk_lookup` index; the null-heal EXISTS pair
// travels through the person_credits_media index (media_type,tmdb_media_id).
func (r *SeriesRepository) PickRefreshCandidates(
	ctx context.Context,
	now time.Time,
	ttl enrichment.RefreshTTL,
	limit int,
) ([]RefreshCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	hotCutoff := now.UTC().Add(-ttl.Hot)
	normalCutoff := now.UTC().Add(-ttl.Normal)
	coldCutoff := now.UTC().Add(-ttl.Cold)
	// W17-1 poster-branch race guard: a series stamped inside the last
	// 15 minutes may be mid-Handle (poster seed not yet committed) — do
	// not yank it into a concurrent refresh.
	posterGuardCutoff := now.UTC().Add(-15 * time.Minute)
	// #1090b null-heal cooldown: HandleForced stamps
	// enrichment_tmdb_synced_at=now on every heal, so this 6h backoff
	// bounds a genuinely-unfillable series (all tv rows stay NULL) to at
	// most one re-pick per 6h instead of once per 30-min tick. Comfortably
	// longer than the tick; short enough that the library (synced across
	// the last 7d) drains on the first ticks.
	healGuardCutoff := now.UTC().Add(-6 * time.Hour)

	const errSrc = "tmdb_series"
	const sqlTmpl = `
SELECT * FROM (
  SELECT s.id AS series_id, 1 AS tier, s.enrichment_tmdb_synced_at AS synced_at,
         CASE WHEN NOT EXISTS (
                SELECT 1 FROM series_media_texts smt
                 WHERE smt.series_id = s.id AND smt.poster_asset IS NOT NULL)
              AND s.enrichment_tmdb_synced_at IS NOT NULL
              AND s.enrichment_tmdb_synced_at >= ?
              THEN 1 ELSE 0 END AS missing_poster
    FROM series s
   WHERE s.tmdb_id IS NOT NULL
     AND (
           s.enrichment_tmdb_synced_at IS NULL
        OR s.enrichment_tmdb_synced_at < ?
        OR (s.enrichment_tmdb_synced_at < ?
            AND NOT EXISTS (
              SELECT 1 FROM series_media_texts smt
               WHERE smt.series_id = s.id AND smt.poster_asset IS NOT NULL))
        OR (s.enrichment_tmdb_synced_at < ?
            AND EXISTS (
              SELECT 1 FROM person_credits pc
               WHERE pc.media_type = 'tv' AND pc.tmdb_media_id = s.tmdb_id)
            AND NOT EXISTS (
              SELECT 1 FROM person_credits pc2
               WHERE pc2.media_type = 'tv' AND pc2.tmdb_media_id = s.tmdb_id
                 AND pc2.last_appearance_season IS NOT NULL))
         )
     AND EXISTS (
       SELECT 1 FROM series_cache sc
        WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
     AND NOT EXISTS (
       SELECT 1 FROM enrichment_errors ee
        WHERE ee.entity_type = 'series' AND ee.entity_id = s.id
          AND ee.source = ? AND ee.attempts > 5)
  UNION ALL
  SELECT s.id, 2, s.enrichment_tmdb_synced_at, 0
    FROM series s
   WHERE s.tmdb_id IS NOT NULL
     AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?)
     AND NOT EXISTS (
       SELECT 1 FROM series_cache sc
        WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
     AND EXISTS (
       SELECT 1 FROM discovery_lists dl WHERE dl.series_id = s.id)
     AND NOT EXISTS (
       SELECT 1 FROM enrichment_errors ee
        WHERE ee.entity_type = 'series' AND ee.entity_id = s.id
          AND ee.source = ? AND ee.attempts > 5)
  UNION ALL
  SELECT s.id, 3, s.enrichment_tmdb_synced_at, 0
    FROM series s
   WHERE s.tmdb_id IS NOT NULL
     AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?)
     AND NOT EXISTS (
       SELECT 1 FROM series_cache sc
        WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
     AND NOT EXISTS (
       SELECT 1 FROM discovery_lists dl WHERE dl.series_id = s.id)
     AND NOT EXISTS (
       SELECT 1 FROM enrichment_errors ee
        WHERE ee.entity_type = 'series' AND ee.entity_id = s.id
          AND ee.source = ? AND ee.attempts > 5)
) u
ORDER BY u.tier ASC,
         COALESCE(u.synced_at, ?) ASC,
         u.series_id ASC
LIMIT ?
`
	// Sentinel for NULL synced_at — placed before every real timestamp
	// so newly-imported series jump to the front of the queue within
	// their tier.
	nullSentinel := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	type row struct {
		SeriesID      domain.SeriesID `gorm:"column:series_id"`
		Tier          int             `gorm:"column:tier"`
		SyncedAt      *time.Time      `gorm:"column:synced_at"`
		MissingPoster int             `gorm:"column:missing_poster"`
	}
	var rows []row
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlTmpl,
			hotCutoff, hotCutoff, posterGuardCutoff, healGuardCutoff, errSrc,
			normalCutoff, errSrc,
			coldCutoff, errSrc,
			nullSentinel, limit,
		).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("pick refresh candidates: %w", err)
	}
	out := make([]RefreshCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, RefreshCandidate{
			SeriesID:      r.SeriesID,
			Tier:          enrichment.RefreshTier(r.Tier),
			SyncedAt:      r.SyncedAt,
			MissingPoster: r.MissingPoster == 1,
		})
	}
	return out, nil
}
