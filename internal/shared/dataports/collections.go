package dataports

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CollectionSeriesRow is one owned series in a curated collection: a live
// series_cache membership row whose canonical series carries at least one of
// the collection's TMDB keyword ids. Title is COALESCE(series.original_title,”)
// — mirrors the gaps/stats/smartlists series-title projection.
type CollectionSeriesRow struct {
	SeriesID domain.SeriesID
	SonarrID domain.SonarrSeriesID
	Title    string
}

// CollectionResult bundles the exact owned total with the bounded (top-N by
// title) series slice for one collection query. OwnedCount is the authoritative
// COUNT(DISTINCT series_id) and is INDEPENDENT of the (capped) Series length.
type CollectionResult struct {
	OwnedCount int
	Series     []CollectionSeriesRow
}

// CollectionsRepository surfaces the read-only "curated collections" queries
// backing GET /api/v1/insights/collections. It is GENERIC: the curated
// slug/title/is_franchise/keyword-id definitions live in the app usecase; this
// port only answers "how many / which owned series match this set of TMDB
// keyword ids". Every query is a bounded COUNT / top-N over EXISTING tables
// (series_cache / series / series_keywords / keywords); no writes, no migration.
// SQL is dialect-portable — the keyword ids bind as a []int64 IN-list, and only
// COALESCE / IN / GROUP BY / ORDER BY / LIMIT appear (no NOW()/INTERVAL/FILTER/
// cast) so the SQLite test lane and the Postgres prod target agree. Every query
// is instance-scoped (series_cache.instance_name = ? AND deleted_at IS NULL).
type CollectionsRepository interface {
	// DistinctInstances lists instance_name values with at least one live
	// series_cache row (deleted_at IS NULL), ordered ascending.
	DistinctInstances(ctx context.Context) ([]string, error)

	// Collection returns the exact owned total plus up to `limit` matching
	// series (ordered by title ASC, series_id ASC tiebreak) for the given set
	// of TMDB keyword ids in one instance. A series counts once no matter how
	// many of the ids it matches (COUNT/GROUP BY DISTINCT on series_id). An
	// empty tmdbIDs slice returns a zero result without querying.
	Collection(ctx context.Context, instance string, tmdbIDs []int64, limit int) (CollectionResult, error)
}
