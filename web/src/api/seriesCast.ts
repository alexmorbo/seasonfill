import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesCastResponse = components['schemas']['dto.SeriesCastResponse'];
export type CastPageMember = components['schemas']['dto.CastPageMember'];
export type CrewPageMember = components['schemas']['dto.CrewPageMember'];

export interface UseSeriesCastParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
  // Story 1087a — optional cast cap. >0 asks the BE for the top-N cast by
  // episode_count (the detail-page strip passes 8); omit/0 for the full list
  // (the dedicated cast page). Folded into the query key so the capped and
  // full responses never collide in the React-Query cache.
  readonly limit?: number | undefined;
}

export function seriesCastQueryKey(
  seriesId: number,
  lang: string,
  limit = 0,
): readonly [string, number, string, number] {
  return ['series-cast', seriesId, lang, limit] as const;
}

export function useSeriesCast({
  seriesId,
  lang,
  limit,
}: UseSeriesCastParams): UseQueryResult<SeriesCastResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  const effectiveLimit = typeof limit === 'number' && limit > 0 ? limit : 0;
  return useQuery<SeriesCastResponse>({
    queryKey: ready
      ? seriesCastQueryKey(seriesId as number, effectiveLang, effectiveLimit)
      : (['series-cast', 0, '', 0] as const),
    queryFn: () => {
      const params = new URLSearchParams();
      if (effectiveLang) params.set('lang', effectiveLang);
      if (effectiveLimit > 0) params.set('limit', String(effectiveLimit));
      const qs = params.toString() ? `?${params.toString()}` : '';
      return api<SeriesCastResponse>(`/series/${seriesId}/cast${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
