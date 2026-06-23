// Package rest — seriesdetail HTTP handlers.
//
// global_helpers.go (Story 492 / N-1b). Shared helper used by the
// global series-scoped wrappers (cast, season, torrents). Resolves the
// preferred (instance, sonarr_id) from the cache by canonical
// series.id, with a deterministic lex-first preference rule so the
// same canonical id always lands on the same instance across calls.
package rest

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// resolvePreferredCacheEntry looks up series_cache rows for the
// canonical series.id and returns the lex-first-by-instance entry plus
// an ok flag (false when zero rows). Used by the global series-scoped
// wrappers to resolve which per-instance handler invocation to
// delegate to. Sorting is performed here so the result is
// deterministic across runs regardless of repository iteration order.
func resolvePreferredCacheEntry(
	ctx context.Context,
	repo seriesdetail.SeriesCacheLookupPort,
	seriesID domain.SeriesID,
) (series.CacheEntry, bool, error) {
	entries, err := repo.ListBySeriesID(ctx, seriesID)
	if err != nil {
		return series.CacheEntry{}, false, fmt.Errorf("resolve preferred cache entry: %w", err)
	}
	if len(entries) == 0 {
		return series.CacheEntry{}, false, nil
	}
	preferred := entries[0]
	for _, e := range entries[1:] {
		if e.InstanceName < preferred.InstanceName {
			preferred = e
		}
	}
	return preferred, true, nil
}

// setParam replaces an existing c.Params entry by key, or appends it
// when absent. gin's Params.Get walks front-to-back and returns the
// FIRST match, so a plain append on a key that already exists in the
// URL path (e.g. `:id`) is a no-op — the inner handler would still
// observe the original value. setParam guarantees the inner handler
// sees the new value, which is what the global wrappers need when
// rewriting `:id` from canonical series.id to the per-instance
// sonarr_series_id.
func setParam(params gin.Params, key, value string) gin.Params {
	for i := range params {
		if params[i].Key == key {
			params[i].Value = value
			return params
		}
	}
	return append(params, gin.Param{Key: key, Value: value})
}
