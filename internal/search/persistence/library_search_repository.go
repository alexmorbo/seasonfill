// Package persistence implements the universal-search read ports (ADR-0024
// S1.2) over the 000067 GIN trgm indexes. Every method dialect-branches on
// db.Dialector.Name(): Postgres uses the index-assisted predicate
// `lower(f_unaccent(col)) LIKE lower(f_unaccent(?))` (BYTE-IDENTICAL to the
// index expression in 000067 — a mismatch makes the planner ignore the GIN)
// ranked by similarity() DESC; SQLite falls back to plain LOWER LIKE ranked
// exact-prefix-first (no f_unaccent/similarity — those do not exist on SQLite).
//
// Query-length branch (F-12): the trigram operator class needs >= 3 chars to
// index a substring match. For q of length < 3 the Postgres path uses a
// bounded PREFIX LIKE (`q%`) with no similarity() call (ranked by popularity
// only) — this short-query branch is NOT trigram-index-assisted (it is a
// LIMIT-bounded scan), which is acceptable for the 2-char instant path. The
// SQLite path is uniform (its LIKE is never index-assisted anyway).
package persistence

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// minTrigramLen is the shortest query that uses the substring/trigram branch
// on Postgres. Shorter queries use the bounded prefix branch (F-12).
const minTrigramLen = 3

// LibrarySearchRepository implements app.LibrarySearchRepository. Construct via
// NewLibrarySearchRepository; thread-safe (stateless GORM wrapper).
type LibrarySearchRepository struct {
	db *gorm.DB
}

// NewLibrarySearchRepository binds the repo to a *gorm.DB. db MUST be non-nil
// at production wiring — panics so a wiring bug surfaces at boot.
func NewLibrarySearchRepository(db *gorm.DB) *LibrarySearchRepository {
	if db == nil {
		panic("library search repository: db required")
	}
	return &LibrarySearchRepository{db: db}
}

func (r *LibrarySearchRepository) isPostgres() bool {
	return r.db.Name() == "postgres"
}

// --- scan targets (column order matches each SELECT verbatim) ---

type seriesHitRow struct {
	ID            int64   `gorm:"column:id"`
	TMDBID        *int64  `gorm:"column:tmdb_id"`
	Title         string  `gorm:"column:title"`
	Year          *int    `gorm:"column:year"`
	PosterAsset   *string `gorm:"column:poster_asset"`
	BackdropAsset *string `gorm:"column:backdrop_asset"`
}

type movieHitRow struct {
	ID            int64   `gorm:"column:id"`
	TMDBID        *int64  `gorm:"column:tmdb_id"`
	Title         string  `gorm:"column:title"`
	Year          *int    `gorm:"column:year"`
	PosterAsset   *string `gorm:"column:poster_asset"`
	BackdropAsset *string `gorm:"column:backdrop_asset"`
}

// ============================ SERIES ============================

// SearchSeries matches series_texts.title across all languages (mirror of
// discovery search.go), display title resolved requested-lang → en-US →
// series.original_title. Ranked by trigram similarity then popularity.
func (r *LibrarySearchRepository) SearchSeries(ctx context.Context, q, language string, limit int) ([]searchdomain.SeriesHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var rows []seriesHitRow
	var err error
	if r.isPostgres() {
		if len([]rune(q)) >= minTrigramLen {
			err = r.seriesPostgresTrigram(ctx, q, language, limit, &rows)
		} else {
			err = r.seriesPostgresPrefix(ctx, q, language, limit, &rows)
		}
	} else {
		err = r.seriesSQLite(ctx, q, language, limit, &rows)
	}
	if err != nil {
		return nil, fmt.Errorf("library search series: %w", err)
	}

	out := make([]searchdomain.SeriesHit, 0, len(rows))
	for _, row := range rows {
		out = append(out, seriesHitFromRow(row))
	}
	return out, nil
}

const seriesDisplayCols = `
       COALESCE((SELECT st.title FROM series_texts st WHERE st.series_id = s.id
                  ORDER BY CASE WHEN st.language = ? THEN 2 WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC,
                           st.language ASC LIMIT 1), s.original_title) AS title,
       s.year,
       (SELECT smt.poster_asset FROM series_media_texts smt WHERE smt.series_id = s.id
          AND smt.poster_asset IS NOT NULL AND smt.poster_asset <> ''
          AND (smt.language = ? OR smt.language = 'en-US')
          ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
                   smt.language ASC LIMIT 1) AS poster_asset,
       (SELECT smt.backdrop_asset FROM series_media_texts smt WHERE smt.series_id = s.id
          AND smt.backdrop_asset IS NOT NULL AND smt.backdrop_asset <> ''
          AND (smt.language = ? OR smt.language = 'en-US')
          ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
                   smt.language ASC LIMIT 1) AS backdrop_asset`

func (r *LibrarySearchRepository) seriesPostgresTrigram(ctx context.Context, q, language string, limit int, rows *[]seriesHitRow) error {
	pattern := "%" + q + "%"
	sql := `
SELECT s.id, s.tmdb_id,` + seriesDisplayCols + `,
       (SELECT MAX(similarity(lower(f_unaccent(st.title)), lower(f_unaccent(?))))
          FROM series_texts st WHERE st.series_id = s.id) AS score
  FROM series s
 WHERE EXISTS (SELECT 1 FROM series_texts st
                WHERE st.series_id = s.id
                  AND lower(f_unaccent(st.title)) LIKE lower(f_unaccent(?)))
 ORDER BY score DESC NULLS LAST, s.popularity DESC NULLS LAST, s.id ASC
 LIMIT ?`
	// args: language(title), language,language(poster), language,language(backdrop), q(score), pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, language, language, language, language, q, pattern, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) seriesPostgresPrefix(ctx context.Context, q, language string, limit int, rows *[]seriesHitRow) error {
	prefix := q + "%"
	sql := `
SELECT s.id, s.tmdb_id,` + seriesDisplayCols + `
  FROM series s
 WHERE EXISTS (SELECT 1 FROM series_texts st
                WHERE st.series_id = s.id
                  AND lower(f_unaccent(st.title)) LIKE lower(f_unaccent(?)))
 ORDER BY s.popularity DESC NULLS LAST, s.id ASC
 LIMIT ?`
	// args: language x5, prefix(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, language, language, language, language, prefix, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) seriesSQLite(ctx context.Context, q, language string, limit int, rows *[]seriesHitRow) error {
	pattern := "%" + q + "%"
	prefix := q + "%"
	sql := `
SELECT s.id, s.tmdb_id,` + seriesDisplayCols + `,
       (SELECT MAX(CASE WHEN LOWER(st.title) LIKE LOWER(?) THEN 1 ELSE 0 END)
          FROM series_texts st WHERE st.series_id = s.id) AS prefix_hit
  FROM series s
 WHERE EXISTS (SELECT 1 FROM series_texts st
                WHERE st.series_id = s.id
                  AND LOWER(st.title) LIKE LOWER(?))
 ORDER BY prefix_hit DESC, s.popularity DESC NULLS LAST, s.id ASC
 LIMIT ?`
	// args: language x5, prefix(prefix_hit), pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, language, language, language, language, prefix, pattern, limit).
		Scan(rows).Error
}

func seriesHitFromRow(row seriesHitRow) searchdomain.SeriesHit {
	hit := searchdomain.SeriesHit{
		SeriesID: shareddomain.SeriesID(row.ID),
		Title:    row.Title,
		Source:   searchdomain.SourceLibrary,
	}
	if row.TMDBID != nil {
		v := shareddomain.TMDBID(*row.TMDBID)
		hit.TMDBID = &v
	}
	if row.Year != nil {
		y := *row.Year
		hit.Year = &y
	}
	if row.PosterAsset != nil && *row.PosterAsset != "" {
		v := *row.PosterAsset
		hit.PosterPath = &v
	}
	if row.BackdropAsset != nil && *row.BackdropAsset != "" {
		v := *row.BackdropAsset
		hit.BackdropPath = &v
	}
	return hit
}

// ============================ MOVIES ============================

// SearchMovies matches movies.title ∪ movies.original_title ∪ movie_i18n.title
// (additive, F-03 — unenriched movies stay findable via the guaranteed canon
// title). Display title resolved requested-lang → en-US → movies.title.
func (r *LibrarySearchRepository) SearchMovies(ctx context.Context, q, language string, limit int) ([]searchdomain.MovieHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var rows []movieHitRow
	var err error
	if r.isPostgres() {
		if len([]rune(q)) >= minTrigramLen {
			err = r.moviePostgresTrigram(ctx, q, language, limit, &rows)
		} else {
			err = r.moviePostgresPrefix(ctx, q, language, limit, &rows)
		}
	} else {
		err = r.movieSQLite(ctx, q, language, limit, &rows)
	}
	if err != nil {
		return nil, fmt.Errorf("library search movies: %w", err)
	}

	out := make([]searchdomain.MovieHit, 0, len(rows))
	for _, row := range rows {
		out = append(out, movieHitFromRow(row))
	}
	return out, nil
}

const movieDisplayCols = `
       COALESCE((SELECT mi.title FROM movie_i18n mi
                  WHERE mi.movie_id = m.id AND mi.title IS NOT NULL
                  ORDER BY CASE WHEN mi.language = ? THEN 2 WHEN mi.language = 'en-US' THEN 1 ELSE 0 END DESC,
                           mi.language ASC LIMIT 1), m.title) AS title,
       m.year, m.poster_asset, m.backdrop_asset`

func (r *LibrarySearchRepository) moviePostgresTrigram(ctx context.Context, q, language string, limit int, rows *[]movieHitRow) error {
	pattern := "%" + q + "%"
	sql := `
SELECT m.id, m.tmdb_id,` + movieDisplayCols + `,
       GREATEST(
         similarity(lower(f_unaccent(m.title)), lower(f_unaccent(?))),
         COALESCE(similarity(lower(f_unaccent(m.original_title)), lower(f_unaccent(?))), 0),
         COALESCE((SELECT MAX(similarity(lower(f_unaccent(mi.title)), lower(f_unaccent(?))))
                     FROM movie_i18n mi WHERE mi.movie_id = m.id), 0)
       ) AS score
  FROM movies m
 WHERE lower(f_unaccent(m.title)) LIKE lower(f_unaccent(?))
    OR lower(f_unaccent(m.original_title)) LIKE lower(f_unaccent(?))
    OR EXISTS (SELECT 1 FROM movie_i18n mi2 WHERE mi2.movie_id = m.id
                 AND lower(f_unaccent(mi2.title)) LIKE lower(f_unaccent(?)))
 ORDER BY score DESC NULLS LAST, m.popularity DESC NULLS LAST, m.id ASC
 LIMIT ?`
	// args: language(title), q,q,q(score), pattern,pattern,pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, q, q, q, pattern, pattern, pattern, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) moviePostgresPrefix(ctx context.Context, q, language string, limit int, rows *[]movieHitRow) error {
	prefix := q + "%"
	sql := `
SELECT m.id, m.tmdb_id,` + movieDisplayCols + `
  FROM movies m
 WHERE lower(f_unaccent(m.title)) LIKE lower(f_unaccent(?))
    OR lower(f_unaccent(m.original_title)) LIKE lower(f_unaccent(?))
    OR EXISTS (SELECT 1 FROM movie_i18n mi2 WHERE mi2.movie_id = m.id
                 AND lower(f_unaccent(mi2.title)) LIKE lower(f_unaccent(?)))
 ORDER BY m.popularity DESC NULLS LAST, m.id ASC
 LIMIT ?`
	// args: language(title), prefix,prefix,prefix(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, prefix, prefix, prefix, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) movieSQLite(ctx context.Context, q, language string, limit int, rows *[]movieHitRow) error {
	pattern := "%" + q + "%"
	prefix := q + "%"
	sql := `
SELECT m.id, m.tmdb_id,` + movieDisplayCols + `,
       (CASE WHEN LOWER(m.title) LIKE LOWER(?) OR LOWER(m.original_title) LIKE LOWER(?)
                  OR EXISTS (SELECT 1 FROM movie_i18n mi WHERE mi.movie_id = m.id
                               AND LOWER(mi.title) LIKE LOWER(?))
             THEN 1 ELSE 0 END) AS prefix_hit
  FROM movies m
 WHERE LOWER(m.title) LIKE LOWER(?)
    OR LOWER(m.original_title) LIKE LOWER(?)
    OR EXISTS (SELECT 1 FROM movie_i18n mi2 WHERE mi2.movie_id = m.id
                 AND LOWER(mi2.title) LIKE LOWER(?))
 ORDER BY prefix_hit DESC, m.popularity DESC NULLS LAST, m.id ASC
 LIMIT ?`
	// args: language(title), prefix,prefix,prefix(prefix_hit), pattern,pattern,pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, language, prefix, prefix, prefix, pattern, pattern, pattern, limit).
		Scan(rows).Error
}

func movieHitFromRow(row movieHitRow) searchdomain.MovieHit {
	hit := searchdomain.MovieHit{
		MovieID: shareddomain.MovieID(row.ID),
		Title:   row.Title,
		Source:  searchdomain.SourceLibrary,
	}
	if row.TMDBID != nil {
		v := shareddomain.TMDBID(*row.TMDBID)
		hit.TMDBID = &v
	}
	if row.Year != nil {
		y := *row.Year
		hit.Year = &y
	}
	if row.PosterAsset != nil && *row.PosterAsset != "" {
		v := *row.PosterAsset
		hit.PosterPath = &v
	}
	if row.BackdropAsset != nil && *row.BackdropAsset != "" {
		v := *row.BackdropAsset
		hit.BackdropPath = &v
	}
	return hit
}

// ========================= COLLECTIONS =========================

// SearchCollections matches collections.name. Collections have no popularity
// column, so the rank tie-break is name ASC → id ASC (deterministic).
func (r *LibrarySearchRepository) SearchCollections(ctx context.Context, q, language string, limit int) ([]searchdomain.CollectionHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var rows []collectionHitRow
	var err error
	if r.isPostgres() {
		if len([]rune(q)) >= minTrigramLen {
			err = r.collectionPostgresTrigram(ctx, q, limit, &rows)
		} else {
			err = r.collectionPostgresPrefix(ctx, q, limit, &rows)
		}
	} else {
		err = r.collectionSQLite(ctx, q, limit, &rows)
	}
	if err != nil {
		return nil, fmt.Errorf("library search collections: %w", err)
	}

	out := make([]searchdomain.CollectionHit, 0, len(rows))
	for _, row := range rows {
		out = append(out, collectionHitFromRow(row))
	}
	return out, nil
}

type collectionHitRow struct {
	ID               int64   `gorm:"column:id"`
	TMDBCollectionID int64   `gorm:"column:tmdb_collection_id"`
	Name             string  `gorm:"column:name"`
	PosterAsset      *string `gorm:"column:poster_asset"`
	BackdropAsset    *string `gorm:"column:backdrop_asset"`
}

func (r *LibrarySearchRepository) collectionPostgresTrigram(ctx context.Context, q string, limit int, rows *[]collectionHitRow) error {
	pattern := "%" + q + "%"
	sql := `
SELECT c.id, c.tmdb_collection_id, c.name, c.poster_asset, c.backdrop_asset,
       similarity(lower(f_unaccent(c.name)), lower(f_unaccent(?))) AS score
  FROM collections c
 WHERE lower(f_unaccent(c.name)) LIKE lower(f_unaccent(?))
 ORDER BY score DESC NULLS LAST, c.name ASC, c.id ASC
 LIMIT ?`
	// args: q(score), pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, q, pattern, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) collectionPostgresPrefix(ctx context.Context, q string, limit int, rows *[]collectionHitRow) error {
	prefix := q + "%"
	sql := `
SELECT c.id, c.tmdb_collection_id, c.name, c.poster_asset, c.backdrop_asset
  FROM collections c
 WHERE lower(f_unaccent(c.name)) LIKE lower(f_unaccent(?))
 ORDER BY c.name ASC, c.id ASC
 LIMIT ?`
	// args: prefix(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, prefix, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) collectionSQLite(ctx context.Context, q string, limit int, rows *[]collectionHitRow) error {
	pattern := "%" + q + "%"
	prefix := q + "%"
	sql := `
SELECT c.id, c.tmdb_collection_id, c.name, c.poster_asset, c.backdrop_asset,
       (CASE WHEN LOWER(c.name) LIKE LOWER(?) THEN 1 ELSE 0 END) AS prefix_hit
  FROM collections c
 WHERE LOWER(c.name) LIKE LOWER(?)
 ORDER BY prefix_hit DESC, c.name ASC, c.id ASC
 LIMIT ?`
	// args: prefix(prefix_hit), pattern(where), limit
	return r.db.WithContext(ctx).
		Raw(sql, prefix, pattern, limit).
		Scan(rows).Error
}

func collectionHitFromRow(row collectionHitRow) searchdomain.CollectionHit {
	hit := searchdomain.CollectionHit{
		CollectionID: searchdomain.CollectionID(row.ID),
		Name:         row.Name,
		Source:       searchdomain.SourceLibrary,
	}
	// tmdb_collection_id is NOT NULL — always present.
	v := shareddomain.TMDBID(row.TMDBCollectionID)
	hit.TMDBID = &v
	if row.PosterAsset != nil && *row.PosterAsset != "" {
		p := *row.PosterAsset
		hit.PosterPath = &p
	}
	if row.BackdropAsset != nil && *row.BackdropAsset != "" {
		b := *row.BackdropAsset
		hit.BackdropPath = &b
	}
	return hit
}

// ============================ PEOPLE ============================

// SearchPeople matches people.original_name ∪ people_texts.name, restricted to
// persons credited on an in-library title (D7). people.name was dropped in
// 000037 — NEVER referenced. Display name resolves requested-lang → en-US →
// original_name. Ranked by trigram similarity then popularity.
func (r *LibrarySearchRepository) SearchPeople(ctx context.Context, q, language string, limit int) ([]searchdomain.PersonHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var rows []personHitRow
	var err error
	if r.isPostgres() {
		if len([]rune(q)) >= minTrigramLen {
			err = r.peoplePostgresTrigram(ctx, q, language, limit, &rows)
		} else {
			err = r.peoplePostgresPrefix(ctx, q, language, limit, &rows)
		}
	} else {
		err = r.peopleSQLite(ctx, q, language, limit, &rows)
	}
	if err != nil {
		return nil, fmt.Errorf("library search people: %w", err)
	}

	out := make([]searchdomain.PersonHit, 0, len(rows))
	for _, row := range rows {
		out = append(out, personHitFromRow(row))
	}
	return out, nil
}

type personHitRow struct {
	ID                 int64   `gorm:"column:id"`
	TMDBID             *int64  `gorm:"column:tmdb_id"`
	Name               string  `gorm:"column:name"`
	ProfileAsset       *string `gorm:"column:profile_asset"`
	KnownForDepartment *string `gorm:"column:known_for_department"`
}

// peopleDisplayName resolves the localized display name: requested-lang →
// en-US → people.original_name. One `?` placeholder (the requested language).
const peopleDisplayName = `
       COALESCE((SELECT pt.name FROM people_texts pt
                  WHERE pt.person_id = p.id AND pt.name IS NOT NULL
                  ORDER BY CASE WHEN pt.language = ? THEN 2 WHEN pt.language = 'en-US' THEN 1 ELSE 0 END DESC,
                           pt.language ASC LIMIT 1), p.original_name) AS name`

// peopleLibraryRestriction is the D7 predicate — a person surfaces only if
// credited on a tmdb_media_id that is in the library. Applied as the sole WHERE
// clause after the candidate set is materialized via the `matched` CTE, so it
// no longer drives the plan (BUG-2 fix): the selective trgm name match drives.
// No placeholders. NOTE: no leading AND — callers prefix `WHERE `.
const peopleLibraryRestriction = `EXISTS (
         SELECT 1 FROM person_credits pc
          WHERE pc.person_id = p.id
            AND (
                 (pc.media_type = 'movie' AND pc.tmdb_media_id IN (SELECT tmdb_id FROM movies WHERE tmdb_id IS NOT NULL))
              OR (pc.media_type = 'tv'    AND pc.tmdb_media_id IN (SELECT tmdb_id FROM series WHERE tmdb_id IS NOT NULL))
                )
       )`

func (r *LibrarySearchRepository) peoplePostgresTrigram(ctx context.Context, q, language string, limit int, rows *[]personHitRow) error {
	pattern := "%" + q + "%"
	sql := `
WITH matched AS (
    SELECT id AS person_id FROM people
     WHERE lower(f_unaccent(original_name)) LIKE lower(f_unaccent(?))
    UNION
    SELECT person_id FROM people_texts
     WHERE lower(f_unaccent(name)) LIKE lower(f_unaccent(?))
)
SELECT p.id, p.tmdb_id,` + peopleDisplayName + `,
       p.profile_asset, p.known_for_department,
       GREATEST(
         COALESCE(similarity(lower(f_unaccent(p.original_name)), lower(f_unaccent(?))), 0),
         COALESCE((SELECT MAX(similarity(lower(f_unaccent(pt.name)), lower(f_unaccent(?))))
                     FROM people_texts pt WHERE pt.person_id = p.id), 0)
       ) AS score
  FROM people p
  JOIN matched m ON m.person_id = p.id
 WHERE ` + peopleLibraryRestriction + `
 ORDER BY score DESC NULLS LAST, p.popularity DESC NULLS LAST, p.id ASC
 LIMIT ?`
	// bind order: pattern(cte orig), pattern(cte texts), language(display name),
	//             q(score orig), q(score texts), limit
	return r.db.WithContext(ctx).
		Raw(sql, pattern, pattern, language, q, q, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) peoplePostgresPrefix(ctx context.Context, q, language string, limit int, rows *[]personHitRow) error {
	prefix := q + "%"
	sql := `
WITH matched AS (
    SELECT id AS person_id FROM people
     WHERE lower(f_unaccent(original_name)) LIKE lower(f_unaccent(?))
    UNION
    SELECT person_id FROM people_texts
     WHERE lower(f_unaccent(name)) LIKE lower(f_unaccent(?))
)
SELECT p.id, p.tmdb_id,` + peopleDisplayName + `,
       p.profile_asset, p.known_for_department
  FROM people p
  JOIN matched m ON m.person_id = p.id
 WHERE ` + peopleLibraryRestriction + `
 ORDER BY p.popularity DESC NULLS LAST, p.id ASC
 LIMIT ?`
	// bind order: prefix(cte orig), prefix(cte texts), language(display name), limit
	return r.db.WithContext(ctx).
		Raw(sql, prefix, prefix, language, limit).
		Scan(rows).Error
}

func (r *LibrarySearchRepository) peopleSQLite(ctx context.Context, q, language string, limit int, rows *[]personHitRow) error {
	pattern := "%" + q + "%"
	prefix := q + "%"
	sql := `
WITH matched AS (
    SELECT id AS person_id FROM people
     WHERE LOWER(original_name) LIKE LOWER(?)
    UNION
    SELECT person_id FROM people_texts
     WHERE LOWER(name) LIKE LOWER(?)
)
SELECT p.id, p.tmdb_id,` + peopleDisplayName + `,
       p.profile_asset, p.known_for_department,
       (CASE WHEN LOWER(p.original_name) LIKE LOWER(?)
                  OR EXISTS (SELECT 1 FROM people_texts pt WHERE pt.person_id = p.id
                               AND LOWER(pt.name) LIKE LOWER(?))
             THEN 1 ELSE 0 END) AS prefix_hit
  FROM people p
  JOIN matched m ON m.person_id = p.id
 WHERE ` + peopleLibraryRestriction + `
 ORDER BY prefix_hit DESC, p.popularity DESC NULLS LAST, p.id ASC
 LIMIT ?`
	// bind order: pattern(cte orig), pattern(cte texts), language(display name),
	//             prefix(prefix_hit orig), prefix(prefix_hit texts), limit
	return r.db.WithContext(ctx).
		Raw(sql, pattern, pattern, language, prefix, prefix, limit).
		Scan(rows).Error
}

func personHitFromRow(row personHitRow) searchdomain.PersonHit {
	hit := searchdomain.PersonHit{
		PersonID: searchdomain.PersonID(row.ID),
		Name:     row.Name,
		Source:   searchdomain.SourceLibrary,
	}
	if row.TMDBID != nil {
		v := shareddomain.TMDBID(*row.TMDBID)
		hit.TMDBID = &v
	}
	if row.ProfileAsset != nil && *row.ProfileAsset != "" {
		p := *row.ProfileAsset
		hit.ProfilePath = &p
	}
	if row.KnownForDepartment != nil && *row.KnownForDepartment != "" {
		k := *row.KnownForDepartment
		hit.KnownFor = &k
	}
	return hit
}
