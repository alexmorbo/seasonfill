// Package persistence — S-E1 i18n base-lang coverage queries.
//
// I18nCoverageRepository computes, for each of the five per-language text
// tables, how many "relevant" entities carry an en-US (base-lang) row.
// "Relevant" is gated on the parent series having a tmdb_id — this mirrors
// the O-1 deploy gate (100% en-US for TMDB series before any canon DROP in
// S-E3). The periodic collector (cmd/server/loops/i18n_coverage.go) turns
// each (covered, total) pair into the seasonfill_i18n_base_coverage{table}
// gauge.
//
// Read-only. Five bounded COUNT queries per tick; the 5-minute cadence keeps
// the cost negligible on a typical library (~300 series / few-k episodes).
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// baseLanguage is the en-US tag the coverage queries count. Kept local to
// avoid importing shared/locale into the persistence layer (parity with the
// existing fallbackLanguage const in i18n_texts.go).
const baseLanguage = "en-US"

// BaseCoverageRow is one table's coverage tally. Pct is NOT computed here —
// the collector derives it (and handles the Total==0 vacuous-100 case) so
// the repo stays a pure counting surface.
type BaseCoverageRow struct {
	Table   string
	Covered int64
	Total   int64
}

// I18nCoverageRepository runs the five base-lang coverage COUNT queries.
type I18nCoverageRepository struct {
	db *gorm.DB
}

func NewI18nCoverageRepository(db *gorm.DB) *I18nCoverageRepository {
	return &I18nCoverageRepository{db: db}
}

// countPair scans a two-column (total, covered) SELECT into a BaseCoverageRow.
type countPair struct {
	Total   int64
	Covered int64
}

// BaseLangCoverage returns one BaseCoverageRow per table, in a stable order:
// series_texts, series_media_texts, episode_texts, season_texts,
// season_media_texts. Each query counts:
//   - Total   = relevant entities whose parent series has tmdb_id NOT NULL.
//   - Covered = of those, how many have an en-US row in the target table.
//
// The queries are dialect-portable (plain COUNT + EXISTS/JOIN, ? binds) so
// they run identically on Postgres (prod) and the SQLite test lane.
func (r *I18nCoverageRepository) BaseLangCoverage(ctx context.Context) ([]BaseCoverageRow, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	type spec struct {
		table string
		query string
	}
	specs := []spec{
		{
			table: "series_texts",
			query: `SELECT
			  (SELECT COUNT(*) FROM series WHERE tmdb_id IS NOT NULL) AS total,
			  (SELECT COUNT(*) FROM series_texts st
			      JOIN series s ON s.id = st.series_id
			     WHERE s.tmdb_id IS NOT NULL AND st.language = ?) AS covered`,
		},
		{
			table: "series_media_texts",
			query: `SELECT
			  (SELECT COUNT(*) FROM series WHERE tmdb_id IS NOT NULL) AS total,
			  (SELECT COUNT(*) FROM series_media_texts smt
			      JOIN series s ON s.id = smt.series_id
			     WHERE s.tmdb_id IS NOT NULL AND smt.language = ?) AS covered`,
		},
		{
			table: "episode_texts",
			query: `SELECT
			  (SELECT COUNT(*) FROM episodes e
			      JOIN series s ON s.id = e.series_id
			     WHERE s.tmdb_id IS NOT NULL) AS total,
			  (SELECT COUNT(*) FROM episode_texts et
			      JOIN episodes e ON e.id = et.episode_id
			      JOIN series s ON s.id = e.series_id
			     WHERE s.tmdb_id IS NOT NULL AND et.language = ?) AS covered`,
		},
		{
			table: "season_texts",
			query: `SELECT
			  (SELECT COUNT(*) FROM seasons se
			      JOIN series s ON s.id = se.series_id
			     WHERE s.tmdb_id IS NOT NULL) AS total,
			  (SELECT COUNT(*) FROM season_texts stx
			      JOIN seasons se ON se.series_id = stx.series_id
			                     AND se.season_number = stx.season_number
			      JOIN series s ON s.id = stx.series_id
			     WHERE s.tmdb_id IS NOT NULL AND stx.language = ?) AS covered`,
		},
		{
			table: "season_media_texts",
			query: `SELECT
			  (SELECT COUNT(*) FROM seasons se
			      JOIN series s ON s.id = se.series_id
			     WHERE s.tmdb_id IS NOT NULL) AS total,
			  (SELECT COUNT(*) FROM season_media_texts smt
			      JOIN seasons se ON se.series_id = smt.series_id
			                     AND se.season_number = smt.season_number
			      JOIN series s ON s.id = smt.series_id
			     WHERE s.tmdb_id IS NOT NULL AND smt.language = ?) AS covered`,
		},
	}

	rows := make([]BaseCoverageRow, 0, len(specs))
	for _, sp := range specs {
		var cp countPair
		if err := db.Raw(sp.query, baseLanguage).Scan(&cp).Error; err != nil {
			return nil, fmt.Errorf("i18n base coverage (%s): %w", sp.table, err)
		}
		rows = append(rows, BaseCoverageRow{
			Table:   sp.table,
			Covered: cp.Covered,
			Total:   cp.Total,
		})
	}
	return rows, nil
}
