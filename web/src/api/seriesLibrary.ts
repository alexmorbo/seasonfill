import { useQuery, type UseQueryResult } from '@tanstack/react-query';
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
