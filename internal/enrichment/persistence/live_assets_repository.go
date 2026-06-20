// Package persistence — live asset-hash collector for the weekly GC
// media sweep (story 218 E-2).
//
// One method, one transaction: walk every *_asset-bearing column we
// own and union the non-NULL distinct values into a set. Memory cost
// is O(n) hashes; for a typical 300-series library this is ~5000
// strings (≈400 KiB). Fine for a once-a-week sweep.
//
// Story 437 (A-1-11) carried this file out of
// infrastructure/database/repositories into the enrichment vertical-
// slice persistence package along with the rest of the catalog repos.

package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// LiveAssetsRepository implements gc.LiveHashSource.
type LiveAssetsRepository struct {
	db *gorm.DB
}

// NewLiveAssetsRepository constructs the repository bound to db.
func NewLiveAssetsRepository(db *gorm.DB) *LiveAssetsRepository {
	return &LiveAssetsRepository{db: db}
}

// CollectLiveAssetHashes unions every non-NULL hash across the entity
// tables that own an asset column. Returns a set for O(1) membership
// probes in the sweep loop.
//
// Columns covered:
//   - series.poster_asset, series.backdrop_asset
//   - seasons.poster_asset
//   - episodes.still_asset
//   - people.profile_asset
//   - networks.logo_asset
//   - production_companies.logo_asset
//   - series_cache.poster_path, fanart_path, banner_path (transitional)
//
// series_cache columns are read defensively during the 000032 cutover
// transition — once those columns drop, the queries become no-op
// errors (silenced; CollectLiveAssetHashes returns the partial set).
func (r *LiveAssetsRepository) CollectLiveAssetHashes(ctx context.Context) (map[string]struct{}, error) {
	out := make(map[string]struct{}, 8192)
	queries := []string{
		`SELECT poster_asset   FROM series         WHERE poster_asset   IS NOT NULL`,
		`SELECT backdrop_asset FROM series         WHERE backdrop_asset IS NOT NULL`,
		`SELECT poster_asset   FROM seasons        WHERE poster_asset   IS NOT NULL`,
		`SELECT still_asset    FROM episodes       WHERE still_asset    IS NOT NULL`,
		`SELECT profile_asset  FROM people         WHERE profile_asset  IS NOT NULL`,
		`SELECT logo_asset     FROM networks       WHERE logo_asset     IS NOT NULL`,
		`SELECT logo_asset     FROM production_companies WHERE logo_asset IS NOT NULL`,
	}
	for _, q := range queries {
		if err := r.collectInto(ctx, q, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *LiveAssetsRepository) collectInto(ctx context.Context, query string, out map[string]struct{}) error {
	rows, err := r.db.WithContext(ctx).Raw(query).Rows()
	if err != nil {
		return fmt.Errorf("collect live asset hashes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return fmt.Errorf("scan live asset hash: %w", err)
		}
		if h != "" {
			out[h] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate live asset hashes: %w", err)
	}
	return nil
}
