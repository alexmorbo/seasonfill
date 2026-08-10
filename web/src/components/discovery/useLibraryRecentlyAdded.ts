import { useMemo } from 'react';
import { useInstances } from '@/lib/instances';
import { useSeriesCache } from '@/lib/api/seriesCache';
import type { DiscoverySeriesItem } from '@/api/discovery';

export interface LibraryRailResult {
  readonly items: readonly DiscoverySeriesItem[];
  readonly isPending: boolean;
  readonly isError: boolean;
}

// useLibraryRecentlyAdded is a thin wrapper over the EXISTING /series catalog
// list hook (seriesCache.ts) with sort=updated_desc, mapped to the
// DiscoverySeriesItem shape the rail renders. ZERO new BE endpoint (ADR-0017
// S1 decision). series_cache has no created_at, so updated_desc is the closest
// "recently active in library" ordering; true added-at ordering is deferred.
//
// useSeriesCache is per-instance (all-libraries is not a wire option today),
// so we read the first configured instance — the primary library. A
// cross-instance "recently added" needs a BE projection change (out of S1).
export function useLibraryRecentlyAdded(enabled: boolean): LibraryRailResult {
  const inst = useInstances();
  const instance = inst.data?.instances?.[0]?.name ?? null;

  const q = useSeriesCache(
    instance,
    { sort: 'updated_desc', status: 'all', limit: 20 },
    { enabled },
  );

  const items = useMemo<readonly DiscoverySeriesItem[]>(
    () =>
      (q.data?.items ?? []).map((it) => ({
        series_id: it.series_id ?? 0,
        tmdb_id: 0,
        title: it.title,
        in_library_instances: [it.instance_name],
        ...(it.year !== undefined ? { year: it.year } : {}),
        ...(it.poster_hash !== undefined ? { poster_hash: it.poster_hash } : {}),
        ...(it.tmdb_rating !== undefined ? { tmdb_rating: it.tmdb_rating } : {}),
      })),
    [q.data?.items],
  );

  return {
    items,
    isPending: enabled && (inst.isPending || q.isPending),
    isError: inst.isError || q.isError,
  };
}
