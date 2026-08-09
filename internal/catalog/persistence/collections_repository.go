package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CollectionsRepository answers the read-only "curated collections" queries
// backing GET /api/v1/insights/collections. Source of instance membership is
// series_cache (deleted_at IS NULL); the keyword match is a join through
// series_keywords → keywords filtered by keywords.tmdb_id IN (ids); the title
// comes from the joined canonical `series` row. Every query is a bounded
// COUNT(DISTINCT) / top-N over EXISTING tables; no writes, no migration. SQL is
// dialect-portable — the ids bind as a []int64 IN-list, and only COALESCE / IN /
// GROUP BY / ORDER BY / LIMIT appear — so the SQLite test lane and the Postgres
// prod target agree. The curated slug/title/is_franchise/keyword-id definitions
// live in the app usecase (internal/catalog/app/collections); this repo is
// generic on purpose.
type CollectionsRepository struct {
	db *gorm.DB
}

func NewCollectionsRepository(db *gorm.DB) *CollectionsRepository {
	return &CollectionsRepository{db: db}
}

type collectionSeriesRow struct {
	SeriesID domain.SeriesID       `gorm:"column:series_id"`
	SonarrID domain.SonarrSeriesID `gorm:"column:sonarr_id"`
	Title    string                `gorm:"column:title"`
}

// collectionMatchWhere is the shared owned-and-keyword-matching predicate.
// Bind order: instance, then the ids IN-list.
const collectionMatchWhere = `sc.instance_name = ?
   AND sc.deleted_at IS NULL
   AND k.tmdb_id IN (?)`

// DistinctInstances — instance_name values with at least one live series_cache
// row, ordered ascending.
func (r *CollectionsRepository) DistinctInstances(ctx context.Context) ([]string, error) {
	var names []string
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT DISTINCT instance_name
		       FROM series_cache
		      WHERE deleted_at IS NULL
		      ORDER BY instance_name ASC`).
		Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("collections distinct_instances: %w", err)
	}
	return names, nil
}

// Collection — the exact owned total + bounded top-N series slice for one set
// of TMDB keyword ids in one instance. Runs two dialect-portable queries: a
// COUNT(DISTINCT series_id) for the authoritative total (never clipped by the
// cap) and a GROUP BY sc.series_id list for the series (title-ordered, capped).
// An empty tmdbIDs slice short-circuits (an empty SQL IN () is invalid). The
// ids bind as []int64 so GORM's positional IN (?) expansion is unambiguous
// across drivers.
func (r *CollectionsRepository) Collection(ctx context.Context, instance string, tmdbIDs []int64, limit int) (ports.CollectionResult, error) {
	if len(tmdbIDs) == 0 {
		return ports.CollectionResult{Series: []ports.CollectionSeriesRow{}}, nil
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// --- exact owned total (independent of the series cap) ---
	var owned int64
	const countSQL = `
		SELECT COUNT(DISTINCT sc.series_id)
		  FROM series_cache sc
		  JOIN series_keywords sk ON sk.series_id = sc.series_id
		  JOIN keywords k ON k.id = sk.keyword_id
		 WHERE ` + collectionMatchWhere
	if err := db.Raw(countSQL, instance, tmdbIDs).Scan(&owned).Error; err != nil {
		return ports.CollectionResult{}, fmt.Errorf("collections owned_count: %w", err)
	}

	// --- bounded top-N series (title-ordered) ---
	// GROUP BY sc.series_id, sc.sonarr_series_id, s.original_title dedupes a
	// series that matches several ids and keeps COALESCE(s.original_title,'') a
	// function of a grouping column (valid strict-SQL on Postgres; accepted by
	// SQLite since one series = one row). Bind order: instance, ids, limit.
	const listSQL = `
		SELECT sc.series_id AS series_id,
		       sc.sonarr_series_id AS sonarr_id,
		       COALESCE(s.original_title, '') AS title
		  FROM series_cache sc
		  JOIN series s ON s.id = sc.series_id
		  JOIN series_keywords sk ON sk.series_id = sc.series_id
		  JOIN keywords k ON k.id = sk.keyword_id
		 WHERE ` + collectionMatchWhere + `
		 GROUP BY sc.series_id, sc.sonarr_series_id, s.original_title
		 ORDER BY COALESCE(s.original_title, '') ASC, sc.series_id ASC
		 LIMIT ?`

	var rows []collectionSeriesRow
	if err := db.Raw(listSQL, instance, tmdbIDs, limit).Scan(&rows).Error; err != nil {
		return ports.CollectionResult{}, fmt.Errorf("collections series: %w", err)
	}

	series := make([]ports.CollectionSeriesRow, 0, len(rows))
	for _, row := range rows {
		series = append(series, ports.CollectionSeriesRow{
			SeriesID: row.SeriesID,
			SonarrID: row.SonarrID,
			Title:    row.Title,
		})
	}
	return ports.CollectionResult{OwnedCount: int(owned), Series: series}, nil
}

// Ensure interface compliance at compile time.
var _ ports.CollectionsRepository = (*CollectionsRepository)(nil)
