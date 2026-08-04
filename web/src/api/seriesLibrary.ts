import { useQuery, useQueries, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesLibraryResponse = components['schemas']['dto.SeriesLibraryResponse'];

export interface UseSeriesLibraryParams {
  readonly seriesId: number | undefined;
  readonly instance: string | undefined;
}

export function seriesLibraryQueryKey(
  seriesId: number,
  instance: string,
): readonly ['series-library', number, string] {
  return ['series-library', seriesId, instance] as const;
}

// GET /series/:id/library?instance= — Sonarr-scoped state (on-disk strip,
// recent grabs, in-progress). Disabled when the series is TMDB-only
// (`in_library_instances` empty ⇒ `instance` undefined): no Sonarr state
// exists, so the library/recent strips render empty. The response carries no
// `degraded` field (Sonarr state is always live), so it does NOT feed the
// degraded aggregate.
export function useSeriesLibrary({
  seriesId,
  instance,
}: UseSeriesLibraryParams): UseQueryResult<SeriesLibraryResponse> {
  const ready =
    typeof seriesId === 'number' && seriesId > 0 &&
    typeof instance === 'string' && instance.length > 0;
  return useQuery<SeriesLibraryResponse>({
    queryKey: ready
      ? seriesLibraryQueryKey(seriesId as number, instance as string)
      : (['series-library', 0, ''] as const),
    queryFn: () =>
      api<SeriesLibraryResponse>(
        `/series/${seriesId}/library?instance=${encodeURIComponent(instance as string)}`,
      ),
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

// ADR-0012 S5 — per-instance monitored season_numbers, so the per-season caret
// can hide instances where the season is already monitored. Fans out one
// /library query per instance name via useQueries, REUSING seriesLibraryQueryKey
// so the default instance's fetch dedups with the caller's existing
// seasonsLibraryQ (no double network hit). Callers pass ONLY the instances the
// series is actually in (in_library_instances) — an instance lacking the series
// has no /library state and is always kept by the caret's add path anyway.
export function useSeriesLibraryMonitoredByInstance(
  seriesId: number | undefined,
  instanceNames: readonly string[],
): ReadonlyMap<string, ReadonlySet<number>> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const results = useQueries({
    queries: instanceNames.map((name) => ({
      queryKey: ready
        ? seriesLibraryQueryKey(seriesId as number, name)
        : (['series-library', 0, name] as const),
      queryFn: () =>
        api<SeriesLibraryResponse>(
          `/series/${seriesId}/library?instance=${encodeURIComponent(name)}`,
        ),
      enabled: ready && name.length > 0,
      staleTime: 30_000,
      refetchOnWindowFocus: false,
    })),
  });

  const map = new Map<string, Set<number>>();
  instanceNames.forEach((name, i) => {
    const rows = results[i]?.data?.seasons;
    if (!rows) return;
    const set = new Set<number>();
    for (const s of rows) {
      if (typeof s.season_number === 'number' && s.monitored) set.add(s.season_number);
    }
    map.set(name, set);
  });
  return map;
}
