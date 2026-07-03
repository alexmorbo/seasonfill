package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
)

// CatalogCounts is a snapshot of the catalog's row totals. The periodic
// collector (cmd/server/loops/catalog_counts.go) turns each field into the
// matching seasonfill_{series,seasons,episodes}_total gauge.
type CatalogCounts struct {
	Series   int64
	Seasons  int64
	Episodes int64
}

// CatalogCountsRepository runs the three catalog-size COUNT queries.
// Read-only; three bounded COUNT(*) round-trips per tick.
type CatalogCountsRepository struct {
	db *gorm.DB
}

func NewCatalogCountsRepository(db *gorm.DB) *CatalogCountsRepository {
	return &CatalogCountsRepository{db: db}
}

// Counts returns the current row totals of the series, seasons and episodes
// tables. Each is a plain dialect-portable COUNT(*) so the query runs
// identically on Postgres (prod) and the SQLite test lane.
func (r *CatalogCountsRepository) Counts(ctx context.Context) (CatalogCounts, error) {
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)

	var out CatalogCounts
	for _, spec := range []struct {
		table string
		dst   *int64
	}{
		{"series", &out.Series},
		{"seasons", &out.Seasons},
		{"episodes", &out.Episodes},
	} {
		if err := db.Table(spec.table).Count(spec.dst).Error; err != nil {
			return CatalogCounts{}, fmt.Errorf("catalog counts (%s): %w", spec.table, err)
		}
	}
	return out, nil
}
