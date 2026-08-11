package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieRefreshCandidate is one row of the movie tiered picker. Tier labels the
// source bucket (changed/normal); SyncedAt is nullable (NULL = never enriched,
// sorted first within the tier). Movie analog of RefreshCandidate — far simpler
// because movies have no library/followed/discovery membership tables (no
// hot/followed/cold split) and no poster/heal person-credit branches.
type MovieRefreshCandidate struct {
	MovieID  domain.MovieID
	Tier     enrichment.RefreshTier
	SyncedAt *time.Time
}

// movieRefreshRaceGuard is the mid-Handle race window: a movie stamped inside
// the last 15 minutes may be mid-refresh (worker not yet committed), so the
// CHANGED tier does not yank it into a concurrent refresh (mirror of the series
// picker's posterGuardCutoff / CHANGED-arm race guard).
const movieRefreshRaceGuard = 15 * time.Minute

// PickMovieRefreshCandidates returns up to `limit` candidates across the two
// movie tiers, ordered by priority (changed → normal) and within-tier by
// staleness ascending (NULL first, then oldest first).
//
// Tier semantics:
//   - CHANGED (tier 0): tmdb_id IS NOT NULL AND tmdb_changed_at marks the movie
//     as changed-pending, PLUS a 15m mid-Handle race guard. Because the CHANGED
//     arm is STANDALONE (no sibling OR clause catches a NULL sync), the race
//     guard is written `(enrichment_tmdb_synced_at IS NULL OR
//     enrichment_tmdb_synced_at < ?)` — a never-synced changed movie (NULL) must
//     still be picked (a bare `< ?` would evaluate NULL < ts = false forever).
//   - NORMAL (tier 3): tmdb_id IS NOT NULL AND NOT changed-pending AND
//     (enrichment_tmdb_synced_at IS NULL OR enrichment_tmdb_synced_at < now-ttl).
//     ttl is enrichment.RefreshTTL.Normal (reused domain type; Hot/Followed/Cold
//     are ignored — movies have a single non-changed staleness tier for R-4a).
//
// R-A02-analog: the NORMAL arm carries an anti-predicate `AND NOT (<changed-
// pending>)` (column refs only, zero new bind params) so a changed+stale movie
// appears EXACTLY ONCE, in tier 0.
//
// The query is one UNION ALL'd round-trip so the LIMIT applies across the
// priority-ordered union, NOT per-tier ("drain changed first, then normal").
//
// Portable across Postgres + SQLite: literal '1970-01-01' NULL sentinel,
// plain column-vs-column compares (enrichment_tmdb_synced_at < tmdb_changed_at),
// no casts, no JSON aggregation.
//
// NOTE (R-4a scope): unlike the series picker this arm carries NO
// enrichment_errors terminal-failure guard. Movies are far fewer and the movie
// worker does not journal terminal failures in R-4a; a terminal-failure guard
// (needs a movie entity_type/source in enrichment_errors) is deferred to a
// follow-up (L3-3 / R-4b). A permanently-failing movie is bounded by the 15m
// race guard, not re-picked every tick.
func (r *MovieRepository) PickMovieRefreshCandidates(
	ctx context.Context,
	now time.Time,
	ttl enrichment.RefreshTTL,
	limit int,
) ([]MovieRefreshCandidate, error) {
	if limit <= 0 {
		limit = 50
	}
	normalCutoff := now.UTC().Add(-ttl.Normal)
	raceCutoff := now.UTC().Add(-movieRefreshRaceGuard)

	const sqlTmpl = `
SELECT * FROM (
  SELECT m.id AS movie_id, 0 AS tier, m.enrichment_tmdb_synced_at AS synced_at
    FROM movies m
   WHERE m.tmdb_id IS NOT NULL
     AND m.tmdb_changed_at IS NOT NULL
     AND (
           m.enrichment_tmdb_synced_at IS NULL
        OR m.enrichment_tmdb_synced_at < m.tmdb_changed_at)
     AND (
           m.enrichment_tmdb_synced_at IS NULL
        OR m.enrichment_tmdb_synced_at < ?)
  UNION ALL
  SELECT m.id AS movie_id, 3 AS tier, m.enrichment_tmdb_synced_at AS synced_at
    FROM movies m
   WHERE m.tmdb_id IS NOT NULL
     AND NOT (m.tmdb_changed_at IS NOT NULL
              AND (m.enrichment_tmdb_synced_at IS NULL
                   OR m.enrichment_tmdb_synced_at < m.tmdb_changed_at))
     AND (
           m.enrichment_tmdb_synced_at IS NULL
        OR m.enrichment_tmdb_synced_at < ?)
) u
ORDER BY u.tier ASC,
         COALESCE(u.synced_at, ?) ASC,
         u.movie_id ASC
LIMIT ?
`
	nullSentinel := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	type row struct {
		MovieID  domain.MovieID `gorm:"column:movie_id"`
		Tier     int            `gorm:"column:tier"`
		SyncedAt *time.Time     `gorm:"column:synced_at"`
	}
	var rows []row
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlTmpl,
			raceCutoff,   // CHANGED tier-0 15m race guard
			normalCutoff, // NORMAL tier-3 staleness cutoff
			nullSentinel, // NULL synced_at ordering sentinel
			limit,
		).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("pick movie refresh candidates: %w", err)
	}
	out := make([]MovieRefreshCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, MovieRefreshCandidate{
			MovieID:  r.MovieID,
			Tier:     enrichment.RefreshTier(r.Tier),
			SyncedAt: r.SyncedAt,
		})
	}
	return out, nil
}
