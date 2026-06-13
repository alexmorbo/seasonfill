import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeriesCastResponse = components['schemas']['dto.SeriesCastResponse'];
export type CastPageMember = components['schemas']['dto.CastPageMember'];
export type CrewPageMember = components['schemas']['dto.CrewPageMember'];

export interface UseSeriesCastParams {
  readonly instance: string | undefined;
  readonly seriesId: number | undefined;
  readonly lang?: string | undefined;
}

export function seriesCastQueryKey(
  instance: string,
  seriesId: number,
  lang: string,
): readonly [string, string, number, string] {
  return ['series-cast', instance, seriesId, lang] as const;
}

export function useSeriesCast({
  instance,
  seriesId,
  lang,
}: UseSeriesCastParams): UseQueryResult<SeriesCastResponse> {
  const ready = Boolean(instance) && typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  return useQuery<SeriesCastResponse>({
    queryKey: ready
      ? seriesCastQueryKey(instance as string, seriesId as number, effectiveLang)
      : (['series-cast', '', 0, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesCastResponse>(
        `/instances/${encodeURIComponent(instance as string)}/series/${seriesId}/cast${qs}`,
      );
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
