// Package persistence — M-8 enrichment backfill coverage-detail queries.
//
// EnrichmentCoverageRepository computes, in one round-trip group, three
// bounded aggregates the periodic collector
// (cmd/server/loops/enrichment_coverage.go) turns into the
// seasonfill_enrichment_{poster_coverage_ratio,checked_empty_total,
// unenriched_series} gauges. Read-only; no migration; additive to the
// existing library-poster / i18n coverage collectors.
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// EnrichmentCoverage is a single-tick snapshot. The collector derives the
// per-lang ratio (and the total==0 vacuous-1.0 case) so the repo stays a
// pure counting surface.
//
//   - LibraryTotal        = distinct non-deleted library series (denominator).
//   - PosterCoveredByLang = per language, distinct library series with a
//     NON-EMPTY localized poster (#1110 predicate). Absent lang = 0 covered.
//   - CheckedEmpty        = kind → count of #1081b checked-but-empty library
//     markers; keys "poster","backdrop".
//   - Unenriched          = reason → count over the whole series table; keys
//     "no_tmdb_id","never_synced".
type EnrichmentCoverage struct {
	LibraryTotal        int64
	PosterCoveredByLang map[string]int64
	CheckedEmpty        map[string]int64
	Unenriched          map[string]int64
}

// EnrichmentCoverageRepository runs the M-8 coverage-detail queries.
type EnrichmentCoverageRepository struct {
	db *gorm.DB
}

func NewEnrichmentCoverageRepository(db *gorm.DB) *EnrichmentCoverageRepository {
	return &EnrichmentCoverageRepository{db: db}
}

// EnrichmentCoverage runs the four bounded aggregates. All SQL is dialect-
// portable (COUNT(DISTINCT)/EXISTS/GROUP BY, `<>`, `= ”`, no FILTER) so it
// runs identically on Postgres (prod) and the SQLite test lane.
func (r *EnrichmentCoverageRepository) EnrichmentCoverage(ctx context.Context) (EnrichmentCoverage, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	out := EnrichmentCoverage{
		PosterCoveredByLang: map[string]int64{},
		CheckedEmpty:        map[string]int64{},
		Unenriched:          map[string]int64{},
	}

	// 1. Library denominator — same scope as seasonfill_library_poster_total.
	var tot struct{ Total int64 }
	if err := db.Raw(
		`SELECT COUNT(DISTINCT sc.series_id) AS total
		   FROM series_cache sc
		  WHERE sc.deleted_at IS NULL`,
	).Scan(&tot).Error; err != nil {
		return EnrichmentCoverage{}, fmt.Errorf("enrichment coverage total: %w", err)
	}
	out.LibraryTotal = tot.Total

	// 2. Per-lang covered — distinct library series with a NON-EMPTY poster.
	type langCount struct {
		Language string
		Covered  int64
	}
	var lc []langCount
	if err := db.Raw(
		`SELECT smt.language AS language,
		        COUNT(DISTINCT sc.series_id) AS covered
		   FROM series_cache sc
		   JOIN series_media_texts smt ON smt.series_id = sc.series_id
		  WHERE sc.deleted_at IS NULL
		    AND smt.poster_asset IS NOT NULL
		    AND smt.poster_asset <> ''
		  GROUP BY smt.language`,
	).Scan(&lc).Error; err != nil {
		return EnrichmentCoverage{}, fmt.Errorf("enrichment coverage poster: %w", err)
	}
	for _, row := range lc {
		out.PosterCoveredByLang[row.Language] = row.Covered
	}

	// 3. #1081b checked-but-empty library markers (poster + backdrop). EXISTS
	//    (not JOIN) keeps COUNT(*) fan-out-safe under multi-instance cache.
	var ce struct {
		Poster   int64
		Backdrop int64
	}
	if err := db.Raw(
		`SELECT
		   (SELECT COUNT(*) FROM series_media_texts smt
		     WHERE (smt.poster_asset IS NULL OR smt.poster_asset = '')
		       AND smt.poster_checked_at IS NOT NULL
		       AND EXISTS (SELECT 1 FROM series_cache sc
		                    WHERE sc.series_id = smt.series_id
		                      AND sc.deleted_at IS NULL)) AS poster,
		   (SELECT COUNT(*) FROM series_media_texts smt
		     WHERE (smt.backdrop_asset IS NULL OR smt.backdrop_asset = '')
		       AND smt.backdrop_checked_at IS NOT NULL
		       AND EXISTS (SELECT 1 FROM series_cache sc
		                    WHERE sc.series_id = smt.series_id
		                      AND sc.deleted_at IS NULL)) AS backdrop`,
	).Scan(&ce).Error; err != nil {
		return EnrichmentCoverage{}, fmt.Errorf("enrichment coverage checked-empty: %w", err)
	}
	out.CheckedEmpty["poster"] = ce.Poster
	out.CheckedEmpty["backdrop"] = ce.Backdrop

	// 4. Unenriched series split by reason (whole-series scope, mirrors
	//    ListMissingTMDBSync). never_synced == enrichment_cold_start_remaining.
	var un struct {
		NoTmdbID    int64
		NeverSynced int64
	}
	if err := db.Raw(
		`SELECT
		   (SELECT COUNT(*) FROM series WHERE tmdb_id IS NULL) AS no_tmdb_id,
		   (SELECT COUNT(*) FROM series
		     WHERE tmdb_id IS NOT NULL
		       AND enrichment_tmdb_synced_at IS NULL) AS never_synced`,
	).Scan(&un).Error; err != nil {
		return EnrichmentCoverage{}, fmt.Errorf("enrichment coverage unenriched: %w", err)
	}
	out.Unenriched["no_tmdb_id"] = un.NoTmdbID
	out.Unenriched["never_synced"] = un.NeverSynced

	return out, nil
}
