// movie_search.go ships the local-first search repo backing the movie
// discovery handler's local tier (ADR-0024 Ф0 S0.2). Movie analog of
// search.go's SearchRepository. Portable SQL: LOWER + LIKE + NULLS LAST — both
// Postgres and SQLite execute the same plan.
//
// Match target (additive, F-03): canon movies.title ∪ movies.original_title ∪
// movie_i18n.title. Unenriched movies stay findable via the guaranteed canon title.
//
// Displayed title resolution: requested language → en-US → COALESCE fallback to
// canon movies.title (movies ALWAYS carry a canon title).
//
// Ranking: popularity DESC NULLS LAST, tmdb_rating DESC NULLS LAST, id ASC.
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieSearchRepository implements discoapp.MovieSearchRepo. Construct via
// NewMovieSearchRepository; thread-safe (stateless GORM wrapper).
type MovieSearchRepository struct {
	db *gorm.DB
}

// NewMovieSearchRepository binds the repo to a *gorm.DB. db MUST be non-nil at
// production wiring — panics so a wiring bug surfaces at boot, not first query.
func NewMovieSearchRepository(db *gorm.DB) *MovieSearchRepository {
	if db == nil {
		panic("discovery movie search repository: db required")
	}
	return &MovieSearchRepository{db: db}
}

// movieSearchRow is the unexported scan target — column order matches the
// SELECT list of the LocalSearch SQL verbatim.
type movieSearchRow struct {
	ID               int64    `gorm:"column:id"`
	TMDBID           *int64   `gorm:"column:tmdb_id"`
	Title            string   `gorm:"column:title"`
	Year             *int     `gorm:"column:year"`
	PosterAsset      *string  `gorm:"column:poster_asset"`
	BackdropAsset    *string  `gorm:"column:backdrop_asset"`
	OriginalLanguage *string  `gorm:"column:original_language"`
	TMDBRating       *float64 `gorm:"column:tmdb_rating"`
}

// LocalSearch runs the additive local-catalog LIKE lookup. q is wrapped with
// leading + trailing '%' and bound as one parameter (3 times). limit caps the
// row count at the storage layer. Empty q short-circuits to ([], nil).
func (r *MovieSearchRepository) LocalSearch(ctx context.Context, q, language string, limit int) ([]disco.MovieItem, error) {
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + q + "%"
	const sql = `
SELECT m.id, m.tmdb_id,
       COALESCE((SELECT mi.title FROM movie_i18n mi
                  WHERE mi.movie_id = m.id AND mi.title IS NOT NULL
                  ORDER BY CASE WHEN mi.language = ? THEN 2 WHEN mi.language = 'en-US' THEN 1 ELSE 0 END DESC,
                           mi.language ASC LIMIT 1), m.title) AS title,
       m.year, m.poster_asset, m.backdrop_asset, m.original_language, m.tmdb_rating
  FROM movies m
 WHERE LOWER(m.title) LIKE LOWER(?)
    OR LOWER(m.original_title) LIKE LOWER(?)
    OR EXISTS (SELECT 1 FROM movie_i18n mi2
                WHERE mi2.movie_id = m.id AND mi2.title IS NOT NULL
                  AND LOWER(mi2.title) LIKE LOWER(?))
 ORDER BY m.popularity DESC NULLS LAST,
          m.tmdb_rating DESC NULLS LAST,
          m.id ASC
 LIMIT ?`

	var rows []movieSearchRow
	if err := r.db.WithContext(ctx).
		Raw(sql, language, pattern, pattern, pattern, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("discovery movie local search: %w", err)
	}

	out := make([]disco.MovieItem, 0, len(rows))
	for _, row := range rows {
		item := disco.MovieItem{
			MovieID: shareddomain.MovieID(row.ID),
			Title:   row.Title,
		}
		if row.TMDBID != nil {
			v := shareddomain.TMDBID(*row.TMDBID)
			item.TMDBID = &v
		}
		if row.Year != nil {
			y := *row.Year
			item.Year = &y
		}
		if row.PosterAsset != nil && *row.PosterAsset != "" {
			v := *row.PosterAsset
			item.PosterPath = &v
		}
		if row.BackdropAsset != nil && *row.BackdropAsset != "" {
			v := *row.BackdropAsset
			item.BackdropPath = &v
		}
		if row.OriginalLanguage != nil && *row.OriginalLanguage != "" {
			v := *row.OriginalLanguage
			item.OriginalLanguage = &v
		}
		if row.TMDBRating != nil {
			v := *row.TMDBRating
			item.TMDBRating = &v
		}
		out = append(out, item)
	}
	return out, nil
}
