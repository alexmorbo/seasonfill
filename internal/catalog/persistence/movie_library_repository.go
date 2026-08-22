package persistence

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// MovieLibraryRepository backs GET /api/v1/movies (Ф6-R-6b) — the global movie
// library list. Membership source is movie_states (the app-managed radarr
// library-membership projection, PK(instance_name, radarr_movie_id)) JOINed to
// the movies canon on movie_states.movie_id = movies.id. A movie held by
// several radarr instances is DEDUPLICATED to one row (GROUP BY movies.id) with
// its instance memberships aggregated — mirroring the movie-detail model (one
// canon, N library rows) rather than the per-instance series_cache.
//
// Dialect portability (SQLite tests + Postgres prod): boolean OR-aggregates use
// MAX(CASE WHEN col THEN 1 ELSE 0 END) rather than Postgres-only BOOL_OR;
// instance-name aggregation is done in Go (a single follow-up IN query) rather
// than the dialect-specific string_agg/group_concat.
type MovieLibraryRepository struct{ db *gorm.DB }

func NewMovieLibraryRepository(db *gorm.DB) *MovieLibraryRepository {
	return &MovieLibraryRepository{db: db}
}

var _ ports.MovieLibraryRepository = (*MovieLibraryRepository)(nil)

// movieLibraryScan is the grouped-query projection (one row per movies.id).
// Only movies-canon columns are SELECTed — the per-instance aggregates
// (monitored/has_file OR, max size, max updated_at) are computed in Go from
// the follow-up instance-rows query. This is deliberate: SQLite strips column
// affinity from aggregate expressions (MAX(updated_at) comes back as TEXT),
// which database/sql cannot Scan into time.Time; direct movies-canon columns
// keep their affinity and scan cleanly. The state filter still uses SQL
// aggregates in HAVING (no value scanned) and updated_desc still ORDERs BY the
// SQL aggregate (ordering needs no scan).
type movieLibraryScan struct {
	ID          int64
	TMDBID      *int64
	Title       string
	Year        *int
	PosterAsset *string
	Status      *string
	ReleaseDate *time.Time
	TMDBRating  *float64
	IMDBRating  *float64
}

// base builds a fresh grouped query (Table/Joins/Where/Group/Having) each call
// so the count + page passes never share statement state.
func (r *MovieLibraryRepository) base(ctx context.Context, filter ports.MovieLibraryFilter) *gorm.DB {
	q := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("movie_states AS ms").
		Joins("JOIN movies m ON m.id = ms.movie_id").
		Where("ms.deleted_at IS NULL").
		Group("m.id")

	if s := strings.TrimSpace(filter.Search); s != "" {
		pattern := "%" + strings.ToLower(s) + "%"
		q = q.Where(
			"LOWER(m.title) LIKE ? OR LOWER(m.original_title) LIKE ? OR "+
				"EXISTS (SELECT 1 FROM movie_i18n mi WHERE mi.movie_id = m.id "+
				"AND mi.title IS NOT NULL AND LOWER(mi.title) LIKE ?)",
			pattern, pattern, pattern,
		)
	}
	switch filter.State {
	case ports.MovieLibraryStateDownloaded:
		q = q.Having("MAX(CASE WHEN ms.has_file THEN 1 ELSE 0 END) = 1")
	case ports.MovieLibraryStateMissing:
		q = q.Having("MAX(CASE WHEN ms.has_file THEN 1 ELSE 0 END) = 0 AND MAX(CASE WHEN ms.monitored THEN 1 ELSE 0 END) = 1")
	}
	return q
}

const movieLibrarySelect = "m.id AS id, m.tmdb_id AS tmdb_id, m.title AS title, " +
	"m.year AS year, m.poster_asset AS poster_asset, m.status AS status, " +
	"m.release_date AS release_date, m.tmdb_rating AS tmdb_rating, m.imdb_rating AS imdb_rating"

// orderClause maps the sort key to a deterministic ORDER BY (portable NULL
// handling: `(col IS NULL)` boolean orders NULLs last on both dialects). A
// stable m.id tiebreaker keeps pagination deterministic across ties.
func orderClause(sort ports.MovieLibrarySort) string {
	switch sort {
	case ports.MovieLibrarySortTitleAsc:
		return "LOWER(m.title) ASC, m.id ASC"
	case ports.MovieLibrarySortReleaseDesc:
		return "(m.release_date IS NULL) ASC, m.release_date DESC, m.id DESC"
	default: // MovieLibrarySortUpdatedDesc
		return "MAX(ms.updated_at) DESC, m.id DESC"
	}
}

func (r *MovieLibraryRepository) List(ctx context.Context, filter ports.MovieLibraryFilter, sort ports.MovieLibrarySort, limit, offset int) ([]ports.MovieLibraryRow, int, error) {
	// total = COUNT of DISTINCT movies matching the filter (wrap the grouped
	// id-projection in a subquery — GORM's .Count on a grouped query would
	// otherwise return per-group counts).
	sub := r.base(ctx, filter).Select("m.id")
	var total int64
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("(?) AS sub", sub).Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count movie library: %w", err)
	}

	var scans []movieLibraryScan
	if err := r.base(ctx, filter).
		Select(movieLibrarySelect).
		Order(orderClause(sort)).
		Limit(limit).Offset(offset).
		Scan(&scans).Error; err != nil {
		return nil, 0, fmt.Errorf("list movie library: %w", err)
	}
	if len(scans) == 0 {
		return nil, int(total), nil
	}

	// Second pass: fetch every ACTIVE per-instance membership row for the
	// page's movies in ONE query (direct columns → clean scan on both
	// dialects), then aggregate in Go: instance names (sorted), monitored /
	// has_file OR, largest size, newest updated_at.
	movieIDs := make([]int64, 0, len(scans))
	for _, s := range scans {
		movieIDs = append(movieIDs, s.ID)
	}
	type instRow struct {
		MovieID         int64
		InstanceName    string
		Monitored       bool
		HasFile         bool
		SizeOnDiskBytes int64
		UpdatedAt       time.Time
	}
	var instRows []instRow
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("movie_states").
		Select("movie_id, instance_name, monitored, has_file, size_on_disk_bytes, updated_at").
		Where("movie_id IN ? AND deleted_at IS NULL", movieIDs).
		Order("instance_name ASC").
		Scan(&instRows).Error; err != nil {
		return nil, 0, fmt.Errorf("list movie library instances: %w", err)
	}
	type agg struct {
		instances []string
		monitored bool
		hasFile   bool
		size      int64
		updatedAt time.Time
	}
	aggByMovie := make(map[int64]*agg, len(scans))
	for _, ir := range instRows {
		a := aggByMovie[ir.MovieID]
		if a == nil {
			a = &agg{}
			aggByMovie[ir.MovieID] = a
		}
		a.instances = append(a.instances, ir.InstanceName)
		a.monitored = a.monitored || ir.Monitored
		a.hasFile = a.hasFile || ir.HasFile
		if ir.SizeOnDiskBytes > a.size {
			a.size = ir.SizeOnDiskBytes
		}
		if ir.UpdatedAt.After(a.updatedAt) {
			a.updatedAt = ir.UpdatedAt
		}
	}

	out := make([]ports.MovieLibraryRow, 0, len(scans))
	for _, s := range scans {
		row := ports.MovieLibraryRow{
			Title:       s.Title,
			Year:        s.Year,
			PosterAsset: s.PosterAsset,
			Status:      s.Status,
			ReleaseDate: s.ReleaseDate,
			TMDBRating:  s.TMDBRating,
			IMDBRating:  s.IMDBRating,
		}
		if a := aggByMovie[s.ID]; a != nil {
			row.Instances = a.instances
			row.Monitored = a.monitored
			row.HasFile = a.hasFile
			row.SizeOnDisk = a.size
			row.UpdatedAt = a.updatedAt
		}
		if s.TMDBID != nil {
			row.TMDBID = int(*s.TMDBID)
		}
		out = append(out, row)
	}
	return out, int(total), nil
}
