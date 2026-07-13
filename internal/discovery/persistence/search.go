// search.go ships the local search repo backing SearchUseCase.Local
// (story 508 N-2g). Portable SQL: LIKE + LOWER + NULLS LAST. Both
// Postgres and SQLite execute the SAME query plan.
//
// Lookup target:
//
//  1. series.title — every row gets its English title indexed here.
//  2. series_texts.title — the localised title for the user's
//     preferred language. JOIN is via LEFT outer + WHERE OR so a
//     missing translation does not drop the English match.
//
// Ranking:
//
//	ORDER BY popularity DESC NULLS LAST, tmdb_rating DESC NULLS LAST
//
// Both DBs honour `NULLS LAST` since SQLite 3.30 (2019-10) and Postgres
// has always supported it. The handler's `limit` arg caps the result
// slice — defaults to 20 at the use case.
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SearchRepository implements discoapp.SearchRepo. Construct via
// NewSearchRepository; thread-safe (stateless GORM wrapper).
type SearchRepository struct {
	db *gorm.DB
}

// NewSearchRepository binds the repo to a *gorm.DB handle. db MUST be
// non-nil at production wiring — the constructor panics so a wiring
// bug surfaces at boot rather than at first query.
func NewSearchRepository(db *gorm.DB) *SearchRepository {
	if db == nil {
		panic("discovery search repository: db required")
	}
	return &SearchRepository{db: db}
}

// searchRow is the unexported scan target. Matches the column slice of
// the LocalSearch SQL verbatim — adding a column to the SELECT must add
// a field here in the same order.
type searchRow struct {
	ID           int64    `gorm:"column:id"`
	TMDBID       *int64   `gorm:"column:tmdb_id"`
	Title        string   `gorm:"column:title"`
	Year         *int     `gorm:"column:year"`
	PosterPath   *string  `gorm:"column:poster_asset"`
	BackdropPath *string  `gorm:"column:backdrop_asset"`
	Popularity   *float64 `gorm:"column:popularity"`
	TMDBRating   *float64 `gorm:"column:tmdb_rating"`
}

// LocalSearch runs the portable two-table LIKE lookup. q is wrapped
// with leading + trailing `%` and bound as a single parameter. limit
// caps the row count at the storage layer so the use case does not
// allocate an oversized buffer for a popular query like "the".
//
// Trim-zero gate: empty / whitespace-only q returns ([], nil) — the
// handler rejects empties with 400 before reaching the repo, but
// defending the storage layer keeps the repo safe for callers that
// skip the gate.
func (r *SearchRepository) LocalSearch(ctx context.Context, q, language string, limit int) ([]disco.Item, error) {
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + q + "%"
	// S-E3a — canon series.title / poster_asset / backdrop_asset were dropped
	// from the domain (columns now dead). Both the display projection AND the
	// title match resolve from the i18n side-tables (series_texts /
	// series_media_texts) with the requested-language → en-US fallback.
	const sql = `
SELECT s.id, s.tmdb_id,
       COALESCE((SELECT st.title FROM series_texts st WHERE st.series_id = s.id
         ORDER BY CASE WHEN st.language = ? THEN 2 WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC,
                  st.language ASC LIMIT 1), s.original_title) AS title,
       s.year,
       (SELECT smt.poster_asset FROM series_media_texts smt WHERE smt.series_id = s.id
         AND smt.poster_asset IS NOT NULL AND smt.poster_asset <> ''
         ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
                  smt.language ASC LIMIT 1) AS poster_asset,
       (SELECT smt.backdrop_asset FROM series_media_texts smt WHERE smt.series_id = s.id
         AND smt.backdrop_asset IS NOT NULL AND smt.backdrop_asset <> ''
         ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
                  smt.language ASC LIMIT 1) AS backdrop_asset,
       s.popularity, s.tmdb_rating
  FROM series s
 WHERE EXISTS (
         SELECT 1 FROM series_texts st
          WHERE st.series_id = s.id
            AND st.title IS NOT NULL
            AND (st.language = ? OR st.language = 'en-US')
            AND LOWER(st.title) LIKE LOWER(?)
       )
 ORDER BY s.popularity DESC NULLS LAST,
          s.tmdb_rating DESC NULLS LAST,
          s.id ASC
 LIMIT ?`

	var rows []searchRow
	if err := r.db.WithContext(ctx).
		Raw(sql, language, language, language, language, pattern, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("discovery local search: %w", err)
	}

	out := make([]disco.Item, 0, len(rows))
	for _, row := range rows {
		item := disco.Item{
			SeriesID: shareddomain.SeriesID(row.ID),
			Title:    row.Title,
		}
		if row.TMDBID != nil {
			v := shareddomain.TMDBID(*row.TMDBID)
			item.TMDBID = &v
		}
		if row.Year != nil {
			y := *row.Year
			item.Year = &y
		}
		if row.PosterPath != nil && *row.PosterPath != "" {
			v := *row.PosterPath
			item.PosterPath = &v
		}
		if row.BackdropPath != nil && *row.BackdropPath != "" {
			v := *row.BackdropPath
			item.BackdropPath = &v
		}
		if row.TMDBRating != nil {
			v := *row.TMDBRating
			item.TMDBRating = &v
		}
		out = append(out, item)
	}
	return out, nil
}
