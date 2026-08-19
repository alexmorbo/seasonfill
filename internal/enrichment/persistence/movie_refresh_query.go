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
// sorted first within the tier). Heal is true when the row qualifies via the
// S-HEAL rolling i18n-gap branch (a localized ru-RU row that is empty AND due for
// a recheck) — an observability signal, non-exclusive of the tmdb/section OR arms.
type MovieRefreshCandidate struct {
	MovieID  domain.MovieID
	Tier     enrichment.RefreshTier
	SyncedAt *time.Time
	// Heal mirrors the is_gap computed column: 1 when the movie was selected
	// because a non-base localized text row is empty AND past the rolling recheck
	// window. Drives seasonfill_movie_refresh_picked_heal_total. Unlike a one-shot
	// backfill counter its steady-state rate is the genuinely-untranslatable floor
	// (movies whose ru title TMDB never provides re-pick once per recheck window).
	Heal bool
}

// movieRefreshRaceGuard is the mid-Handle race window: a movie stamped inside the
// last 15 minutes may be mid-refresh (worker not yet committed), so the CHANGED
// tier does not yank it into a concurrent refresh (mirror of the series picker's
// posterGuardCutoff / CHANGED-arm race guard).
const movieRefreshRaceGuard = 15 * time.Minute

// movieI18nGapRecheck re-declares the on-view movie text plugin's recheck window
// (movie_text_plugin.go:16 movieTitleGapRecheck, itself mirroring
// moviedetail/app/freshener.go:55). Kept in sync by contract: it MUST equal the
// on-view window so the background picker and the on-view freshener share ONE
// self-healing gap definition (S-HEAL: background and on-view must not diverge).
// After a HandleForced stamps movie_i18n.enriched_at = now, a still-empty
// localized row is re-picked at most once per this window (anti-storm).
const movieI18nGapRecheck = 7 * 24 * time.Hour

// PickMovieRefreshCandidates returns up to `limit` candidates across the two movie
// tiers, ordered by priority (changed → normal), then gap-first, then staleness
// ascending (NULL first, then oldest first).
//
// Tier semantics:
//
//   - CHANGED (tier 0): tmdb_id IS NOT NULL AND tmdb_changed_at marks the movie as
//     changed-pending, PLUS a 15m mid-Handle race guard. Because the CHANGED arm is
//     STANDALONE (no sibling OR catches a NULL sync), the race guard is written
//     `(enrichment_tmdb_synced_at IS NULL OR enrichment_tmdb_synced_at < ?)` — a
//     never-synced changed movie (NULL) must still be picked.
//
//   - NORMAL (tier 3): tmdb_id IS NOT NULL AND NOT changed-pending AND
//     (enrichment_tmdb_synced_at IS NULL OR < now-ttl
//     OR any of enrichment_{cast,keywords,recs,media}_synced_at IS NULL
//     OR <rolling i18n gap>).
//
//     Rolling i18n-gap branch (S-HEAL — replaces the one-shot S3/U-1b guard): the
//     movie is re-picked when there EXISTS a non-base (locale.SupportedUserLanguages
//     minus locale.Default(), currently ["ru-RU"]) movie_i18n row that is
//     (a) empty — NULL/” title OR NULL/” overview — AND (b) DUE for a recheck:
//     enriched_at IS NULL OR enriched_at < now-movieI18nGapRecheck. This is the
//     EXACT shape of MovieI18nReadRepository.HasLocalizedTextGap (movie_i18n_read.go:
//     176-196), so background and on-view heal share one definition:
//
//   - "row EXISTS" gates OUT genuinely-untranslated movies (no ru row at all):
//     TMDB never returned a translation, nothing to heal, so re-fetching is
//     wasted (movie_worker.go:180-184 skips the row for a missing translation).
//
//   - "empty title/overview" targets the U-1 empty-title bug (~10,806 movies
//     carry a non-empty ru overview but an empty title) and stray empty overviews.
//
//   - "enriched_at NULL or < recheck" makes the branch ROLLING and self-healing:
//     UpsertEnriched (movie_worker.go:187) advances movie_i18n.enriched_at every
//     hydrate, so a just-healed-still-empty movie is suppressed for one window,
//     then re-picked — no per-tick storm, but NOT a one-shot dead end.
//
//     NOTE: MarkTextSynced (movie_worker.go:209) still stamps
//     enrichment_text_synced_at unconditionally. That column is NO LONGER read by
//     this picker (the rolling clock is movie_i18n.enriched_at); the stamp is left
//     unchanged because on-read consumers of the coarse text clock still rely on it.
//     genres/companies (no stamp column) are intentionally excluded from the OR.
//
// R-A02-analog: the NORMAL arm carries `AND NOT (<changed-pending>)` (column refs
// only, zero new binds) so a changed+stale movie appears EXACTLY ONCE, in tier 0.
//
// Ordering: is_gap DESC (after tier) floats gap movies ahead of complete TTL churn
// within a tier, so the localization backlog drains even when gap movies carry a
// recent tmdb clock (F-04). is_gap repeats the rolling i18n-gap EXISTS predicate as
// a CASE 0/1 column and is also surfaced as the candidate's Heal flag.
//
// The query is one UNION ALL'd round-trip so LIMIT applies across the priority-
// ordered union ("drain changed first, then normal, gap-first within each").
//
// Portable across Postgres + SQLite: '1970-01-01' NULL sentinel, plain
// column-vs-column compares, and the i18n branch uses only EXISTS, IN (?),
// `<> ”` / `= ”`, IS NULL and a timestamp compare — valid on both engines. No
// casts, no JSON aggregation, `?` binds only.
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
	recheckCutoff := now.UTC().Add(-movieI18nGapRecheck)

	// nonBaseLangs — supported UI languages minus the base (currently ["ru-RU"]).
	// When empty (base is the only UI language) the i18n branch is omitted so the
	// query stays valid (is_gap collapses to the constant 0).
	baseLang := locale.Default()
	nonBaseLangs := make([]string, 0, len(locale.SupportedUserLanguages))
	for _, l := range locale.SupportedUserLanguages {
		if l != baseLang {
			nonBaseLangs = append(nonBaseLangs, l)
		}
	}

	// gapPredicate — the rolling HasLocalizedTextGap-shaped EXISTS. Two binds per
	// use: nonBaseLangs (IN (?)) then recheckCutoff (enriched_at < ?). Used TWICE:
	// as the is_gap CASE column (SELECT list) and as the WHERE OR arm.
	const gapPredicate = `EXISTS (
              SELECT 1 FROM movie_i18n mi
               WHERE mi.movie_id = m.id
                 AND mi.language IN (?)
                 AND (mi.enriched_at IS NULL OR mi.enriched_at < ?)
                 AND ((mi.title IS NULL OR mi.title = '')
                   OR (mi.overview IS NULL OR mi.overview = '')))`

	isGapExpr := "0"
	i18nWhereFragment := ""
	if len(nonBaseLangs) > 0 {
		isGapExpr = "CASE WHEN " + gapPredicate + " THEN 1 ELSE 0 END"
		i18nWhereFragment = "\n        OR " + gapPredicate
	}

	const sqlTmpl = `
SELECT * FROM (
  SELECT m.id AS movie_id, 0 AS tier, m.enrichment_tmdb_synced_at AS synced_at,
         0 AS is_gap
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
  SELECT m.id AS movie_id, 3 AS tier, m.enrichment_tmdb_synced_at AS synced_at,
         %s AS is_gap
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
         u.is_gap DESC,
         COALESCE(u.synced_at, ?) ASC,
         u.movie_id ASC
LIMIT ?
`
	sqlStr := fmt.Sprintf(sqlTmpl, isGapExpr, i18nWhereFragment)
	nullSentinel := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

	// Positional binds, left-to-right in sqlStr:
	//   1. raceCutoff          — CHANGED WHERE race guard (< ?)
	//   2. nonBaseLangs, recheckCutoff — NORMAL SELECT is_gap CASE (IN (?), < ?)  [i18n only]
	//   3. normalCutoff        — NORMAL WHERE staleness (< ?)
	//   4. nonBaseLangs, recheckCutoff — NORMAL WHERE i18n OR (IN (?), < ?)       [i18n only]
	//   5. nullSentinel        — ORDER BY sentinel
	//   6. limit               — LIMIT
	args := make([]any, 0, 10)
	args = append(args, raceCutoff)
	if len(nonBaseLangs) > 0 {
		args = append(args, nonBaseLangs, recheckCutoff)
	}
	args = append(args, normalCutoff)
	if len(nonBaseLangs) > 0 {
		args = append(args, nonBaseLangs, recheckCutoff)
	}
	args = append(args, nullSentinel, limit)

	type row struct {
		MovieID  domain.MovieID `gorm:"column:movie_id"`
		Tier     int            `gorm:"column:tier"`
		SyncedAt *time.Time     `gorm:"column:synced_at"`
		IsGap    int            `gorm:"column:is_gap"`
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
			Heal:     r.IsGap == 1,
		})
	}
	return out, nil
}
