import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesOverviewResponse = components['schemas']['dto.SeriesOverviewResponse'];
export type OverviewAside = components['schemas']['dto.OverviewAside'];

export interface UseSeriesOverviewParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
  readonly pollWhileDegraded?: boolean | undefined;
}

export function seriesOverviewQueryKey(
  seriesId: number,
  lang: string,
): readonly [string, number, string] {
  return ['series-overview', seriesId, lang] as const;
}

const HOT_SOURCES = new Set<string>(['tmdb_series']);
function isHotDegraded(resp: SeriesOverviewResponse | undefined): boolean {
  if (!resp || !resp.degraded || resp.degraded.length === 0) return false;
  return resp.degraded.some((s) => HOT_SOURCES.has(s));
}

export function useSeriesOverview({
  seriesId,
  lang,
  pollWhileDegraded,
}: UseSeriesOverviewParams): UseQueryResult<SeriesOverviewResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  return useQuery<SeriesOverviewResponse>({
    queryKey: ready
      ? seriesOverviewQueryKey(seriesId as number, effectiveLang)
      : (['series-overview', 0, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesOverviewResponse>(`/series/${seriesId}/overview${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    refetchInterval: (q) => {
      if (!pollWhileDegraded) return false;
      return isHotDegraded(q.state.data) ? 4_000 : false;
    },
  });
}
