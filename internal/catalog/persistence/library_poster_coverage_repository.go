package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// LibraryPosterCoverage is a snapshot of library poster coverage. The
// periodic collector (cmd/server/loops/library_poster_coverage.go) turns
// this into the seasonfill_library_poster_{coverage,covered,total} gauges.
// Covered = distinct non-deleted library series (canonical series_id in
// series_cache) that carry a series_media_texts row with a non-NULL
// poster_asset; Total = all distinct non-deleted library series. Counts
// are DISTINCT on the canonical series_id so a series present in multiple
// Sonarr instances (multiple series_cache rows, one canon) is tallied
// once — the absolute gauges are series counts, not row counts.
type LibraryPosterCoverage struct {
	Covered int64
	Total   int64
}

// LibraryPosterCoverageRepository runs the library poster coverage query.
// Read-only; a single two-column round-trip per tick.
type LibraryPosterCoverageRepository struct {
	db *gorm.DB
}

func NewLibraryPosterCoverageRepository(db *gorm.DB) *LibraryPosterCoverageRepository {
	return &LibraryPosterCoverageRepository{db: db}
}

// LibraryPosterCoverage returns the current covered/total tally. The query is
// dialect-portable (COUNT(DISTINCT ...) + EXISTS, no FILTER) so it runs
// identically on Postgres (prod) and the SQLite test lane. Both counts are
// DISTINCT on the canonical series_id so multi-instance duplication of a
// series does not inflate the gauges.
func (r *LibraryPosterCoverageRepository) LibraryPosterCoverage(ctx context.Context) (LibraryPosterCoverage, error) {
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)

	const query = `SELECT
	  (SELECT COUNT(DISTINCT sc.series_id) FROM series_cache sc
	     WHERE sc.deleted_at IS NULL) AS total,
	  (SELECT COUNT(DISTINCT sc.series_id) FROM series_cache sc
	     WHERE sc.deleted_at IS NULL
	       AND EXISTS (SELECT 1 FROM series_media_texts smt
	                   WHERE smt.series_id = sc.series_id
	                     AND smt.poster_asset IS NOT NULL)) AS covered`

	var out LibraryPosterCoverage
	if err := db.Raw(query).Scan(&out).Error; err != nil {
		return LibraryPosterCoverage{}, fmt.Errorf("library poster coverage: %w", err)
	}
	return out, nil
}
