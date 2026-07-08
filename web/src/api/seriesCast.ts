import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesCastResponse = components['schemas']['dto.SeriesCastResponse'];
export type CastPageMember = components['schemas']['dto.CastPageMember'];
export type CrewPageMember = components['schemas']['dto.CrewPageMember'];

// Story 1087b-1 — server-side cast sort. The BE `/cast` endpoint accepts
// `?sort=episodes|name|credit` (default `episodes`) and sorts + ETag-varies by
// it, so the FE no longer re-sorts client-side; it just passes the selected
// key through. (`last_appearance` is deferred to 1087b-2 — BE has no support.)
export type CastSort = 'episodes' | 'credit' | 'name';

export interface UseSeriesCastParams {
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
  // Story 1087a — optional cast cap. >0 asks the BE for the top-N cast by
  // episode_count (the detail-page strip passes 8); omit/0 for the full list
  // (the dedicated cast page). Folded into the query key so the capped and
  // full responses never collide in the React-Query cache.
  readonly limit?: number | undefined;
  // Story 1087b-1 — server sort order. Omitted/undefined → `episodes` (BE
  // default), so the detail-page strip stays unchanged. Folded into the query
  // key so switching the dropdown refetches deterministically (no collision).
  readonly sort?: CastSort | undefined;
}

export function seriesCastQueryKey(
  seriesId: number,
  lang: string,
  limit = 0,
  sort: CastSort = 'episodes',
): readonly [string, number, string, number, CastSort] {
  return ['series-cast', seriesId, lang, limit, sort] as const;
}

export function useSeriesCast({
  seriesId,
  lang,
  limit,
  sort,
}: UseSeriesCastParams): UseQueryResult<SeriesCastResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  const effectiveLimit = typeof limit === 'number' && limit > 0 ? limit : 0;
  const effectiveSort: CastSort = sort ?? 'episodes';
  return useQuery<SeriesCastResponse>({
    queryKey: ready
      ? seriesCastQueryKey(seriesId as number, effectiveLang, effectiveLimit, effectiveSort)
      : (['series-cast', 0, '', 0, 'episodes'] as const),
    queryFn: () => {
      const params = new URLSearchParams();
      if (effectiveLang) params.set('lang', effectiveLang);
      if (effectiveLimit > 0) params.set('limit', String(effectiveLimit));
      if (effectiveSort !== 'episodes') params.set('sort', effectiveSort);
      const qs = params.toString() ? `?${params.toString()}` : '';
      return api<SeriesCastResponse>(`/series/${seriesId}/cast${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
