import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesSeasonsResponse = components['schemas']['dto.SeriesSeasonsResponse'];
export type SeasonSummary = components['schemas']['dto.SeasonSummaryDTO'];

export interface UseSeriesSeasonsParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
  readonly pollWhileDegraded?: boolean | undefined;
}

// queryKey carries lang VERBATIM (BCP-47, no normalization) so a language switch
// is a distinct cache entry — mirrors seriesOverviewQueryKey.
export function seriesSeasonsQueryKey(
  seriesId: number,
  lang: string,
): readonly [string, number, string] {
  return ['series-seasons', seriesId, lang] as const;
}

const HOT_SOURCES = new Set<string>(['tmdb_series']);
function isHotDegraded(resp: SeriesSeasonsResponse | undefined): boolean {
  if (!resp || !resp.degraded || resp.degraded.length === 0) return false;
  return resp.degraded.some((s) => HOT_SOURCES.has(s));
}

export function useSeriesSeasons({
  seriesId,
  lang,
  pollWhileDegraded,
}: UseSeriesSeasonsParams): UseQueryResult<SeriesSeasonsResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  return useQuery<SeriesSeasonsResponse>({
    queryKey: ready
      ? seriesSeasonsQueryKey(seriesId as number, effectiveLang)
      : (['series-seasons', 0, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesSeasonsResponse>(`/series/${seriesId}/seasons${qs}`);
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
