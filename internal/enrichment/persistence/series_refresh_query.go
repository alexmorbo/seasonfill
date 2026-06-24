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
// (NULL = never enriched, sorted first within the tier).
type RefreshCandidate struct {
	SeriesID domain.SeriesID
	Tier     enrichment.RefreshTier
	SyncedAt *time.Time
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
// The query is one UNION ALL'd round-trip so the LIMIT applies across
// the priority-ordered union, NOT per-tier — which is the desired
// budget behaviour ("drain hot first, then normal, then cold").
//
// Portable across Postgres + SQLite: literal '1970-01-01' for NULL
// sentinel ordering, no array_agg, no JSON aggregation. The terminal-
// failure guard is the same NOT EXISTS shape `ListStaleForTMDB` ships
// today (see series_repository.go:391-401) and travels through the
// same `enrichment_errors_pk_lookup` index.
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

	const errSrc = "tmdb_series"
	const sqlTmpl = `
SELECT * FROM (
  SELECT s.id AS series_id, 1 AS tier, s.enrichment_tmdb_synced_at AS synced_at
    FROM series s
   WHERE s.tmdb_id IS NOT NULL
     AND (s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?)
     AND EXISTS (
       SELECT 1 FROM series_cache sc
        WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)
     AND NOT EXISTS (
       SELECT 1 FROM enrichment_errors ee
        WHERE ee.entity_type = 'series' AND ee.entity_id = s.id
          AND ee.source = ? AND ee.attempts > 5)
  UNION ALL
  SELECT s.id, 2, s.enrichment_tmdb_synced_at
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
  SELECT s.id, 3, s.enrichment_tmdb_synced_at
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
		SeriesID domain.SeriesID `gorm:"column:series_id"`
		Tier     int             `gorm:"column:tier"`
		SyncedAt *time.Time      `gorm:"column:synced_at"`
	}
	var rows []row
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlTmpl,
			hotCutoff, errSrc,
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
			SeriesID: r.SeriesID,
			Tier:     enrichment.RefreshTier(r.Tier),
			SyncedAt: r.SyncedAt,
		})
	}
	return out, nil
}
