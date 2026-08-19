package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
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
//
//   - CHANGED (tier 0): tmdb_id IS NOT NULL AND tmdb_changed_at marks the movie
//     as changed-pending, PLUS a 15m mid-Handle race guard. Because the CHANGED
//     arm is STANDALONE (no sibling OR clause catches a NULL sync), the race
//     guard is written `(enrichment_tmdb_synced_at IS NULL OR
//     enrichment_tmdb_synced_at < ?)` — a never-synced changed movie (NULL) must
//     still be picked (a bare `< ?` would evaluate NULL < ts = false forever).
//
//   - NORMAL (tier 3): tmdb_id IS NOT NULL AND NOT changed-pending AND
//     (enrichment_tmdb_synced_at IS NULL OR enrichment_tmdb_synced_at < now-ttl
//     OR any of enrichment_{cast,keywords,recs,media}_synced_at IS NULL
//     OR <i18n-coverage gap>).
//     ttl is enrichment.RefreshTTL.Normal (reused domain type; Hot/Followed/Cold
//     are ignored — movies have a single non-changed staleness tier for R-4a).
//     The NULL-section OR re-picks a movie whose CANON was TMDB-enriched before
//     the Ф1 section-writers existed (fresh tmdb clock, NULL section stamps) so
//     its cast/keywords/recs/media sections get filled. Churn-safe: the movie
//     worker stamps all 4 sections atomically per Handle (empty sections stamped
//     too), so once processed all 4 are non-NULL → the OR is false → not re-picked.
//     genres/companies (no stamp column) are intentionally excluded.
//
//     i18n-coverage branch (S3/U-1b): a movie missing a non-empty overview OR a
//     non-empty TITLE for ANY non-base UI language (locale.SupportedUserLanguages
//     minus locale.Default(), currently ["ru-RU"]) is re-picked so the worker retries
//     the translation fetch — each predicate COUNT(DISTINCT covered) < len(nonBaseLangs)
//     fires as soon as one non-base language lacks the non-empty field (accurate for a
//     future second non-base language; identical to "every" while there is one). The
//     TITLE arm (U-1b) heals the ~10,806 legacy movies that carry a non-empty ru-RU
//     overview but an empty title (the U-1 decode bug); those are activated by MAIN's
//     one-time `enrichment_text_synced_at = NULL` reset so the guard below lets them
//     through exactly once.
//     GUARDED by `enrichment_text_synced_at IS NULL` so it fires AT MOST
//     ONCE per movie: the worker stamps enrichment_text_synced_at on every
//     hydration attempt (even when TMDB has no ru translation), flipping the guard
//     non-NULL — so a movie whose ru overview TMDB never provides is NOT re-picked
//     every tick (anti-storm; mirror of the writeCast "stamp even for empty →
//     stops re-firing" precedent). The non-base language set is passed as a bind
//     (`IN (?)`, GORM-expanded); when it is empty the branch is omitted entirely.
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
// and the i18n branch uses only COUNT(DISTINCT), a scalar subquery, IN (?) and
// `overview <> ”` — all valid on both engines. No casts, no JSON aggregation.
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

	// nonBaseLangs — supported UI languages minus the base (currently ["ru-RU"]).
	// The i18n-coverage branch re-picks a movie whose overview is missing/empty for
	// ANY one of these (COUNT(DISTINCT covered) < len(nonBaseLangs) — fires as soon
	// as one lacks a non-empty overview). When empty (base is the only UI language)
	// the branch is omitted so the query stays valid.
	baseLang := locale.Default()
	nonBaseLangs := make([]string, 0, len(locale.SupportedUserLanguages))
	for _, l := range locale.SupportedUserLanguages {
		if l != baseLang {
			nonBaseLangs = append(nonBaseLangs, l)
		}
	}

	// i18nFragment — the S3/U-1b anti-storm OR-branch, appended to the NORMAL arm's OR
	// block. `enrichment_text_synced_at IS NULL` is the single-re-pick guard: the worker
	// stamps it on every attempt, so once a movie is refreshed the branch is false forever
	// regardless of ru availability. The movie is re-picked when the non-base languages
	// lack a non-empty OVERVIEW (original S3) OR a non-empty TITLE (U-1b: the ~10,806 have
	// the overview but an empty title, so the overview-only check missed them). Four binds:
	// nonBaseLangs + len for the overview subquery, then nonBaseLangs + len for the title
	// subquery (GORM-expanded IN (?)).
	i18nFragment := ""
	if len(nonBaseLangs) > 0 {
		i18nFragment = `
        OR (m.enrichment_text_synced_at IS NULL
            AND ((SELECT COUNT(DISTINCT mi.language)
                    FROM movie_i18n mi
                   WHERE mi.movie_id = m.id
                     AND mi.language IN (?)
                     AND mi.overview IS NOT NULL
                     AND mi.overview <> '') < ?
              OR (SELECT COUNT(DISTINCT mi.language)
                    FROM movie_i18n mi
                   WHERE mi.movie_id = m.id
                     AND mi.language IN (?)
                     AND mi.title IS NOT NULL
                     AND mi.title <> '') < ?))`
	}

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
        OR m.enrichment_tmdb_synced_at < ?
        OR m.enrichment_cast_synced_at IS NULL
        OR m.enrichment_keywords_synced_at IS NULL
        OR m.enrichment_recs_synced_at IS NULL
        OR m.enrichment_media_synced_at IS NULL%s)
) u
ORDER BY u.tier ASC,
         COALESCE(u.synced_at, ?) ASC,
         u.movie_id ASC
LIMIT ?
`
	sqlStr := fmt.Sprintf(sqlTmpl, i18nFragment)
	nullSentinel := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	// Positional binds, left-to-right in sqlStr: raceCutoff (CHANGED race guard),
	// normalCutoff (NORMAL staleness), then — ONLY when the i18n branch is present —
	// nonBaseLangs (IN (?)) + len(nonBaseLangs) for the OVERVIEW subquery, then
	// nonBaseLangs (IN (?)) + len(nonBaseLangs) for the TITLE subquery (U-1b), then
	// nullSentinel (ORDER BY sentinel) and limit.
	args := make([]any, 0, 8)
	args = append(args, raceCutoff, normalCutoff)
	if len(nonBaseLangs) > 0 {
		// overview subquery: (nonBaseLangs, len); title subquery: (nonBaseLangs, len)
		args = append(args, nonBaseLangs, len(nonBaseLangs), nonBaseLangs, len(nonBaseLangs))
	}
	args = append(args, nullSentinel, limit)

	type row struct {
		MovieID  domain.MovieID `gorm:"column:movie_id"`
		Tier     int            `gorm:"column:tier"`
		SyncedAt *time.Time     `gorm:"column:synced_at"`
	}
	var rows []row
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlStr, args...).Scan(&rows).Error
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
