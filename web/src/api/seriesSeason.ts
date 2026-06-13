import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

export type SeasonDetailResponse = components['schemas']['dto.SeasonDetailResponse'];
export type Season = components['schemas']['dto.Season'];
export type Episode = components['schemas']['dto.Episode'];

export interface UseSeriesSeasonParams {
  readonly instance: string | undefined;
  readonly seriesId: number | undefined;
  readonly seasonNumber: number | undefined;
  readonly lang?: string | undefined;
  // Owner-controlled gate. <SeasonsAccordion> sets this true after
  // the user expands the row. Until then we never hit the network.
  readonly enabled: boolean;
}

export function seriesSeasonQueryKey(
  instance: string,
  seriesId: number,
  seasonNumber: number,
  lang: string,
): readonly [string, string, number, number, string] {
  return ['series-season', instance, seriesId, seasonNumber, lang] as const;
}

export function useSeriesSeason({
  instance,
  seriesId,
  seasonNumber,
  lang,
  enabled,
}: UseSeriesSeasonParams): UseQueryResult<SeasonDetailResponse> {
  const ready = Boolean(instance)
    && typeof seriesId === 'number' && seriesId > 0
    && typeof seasonNumber === 'number' && seasonNumber >= 0;
  const effectiveLang = lang ?? '';
  return useQuery<SeasonDetailResponse>({
    queryKey: ready
      ? seriesSeasonQueryKey(instance as string, seriesId as number, seasonNumber as number, effectiveLang)
      : (['series-season', '', 0, -1, ''] as const),
    queryFn: () => {
      const qs = effectiveLang ? `?lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeasonDetailResponse>(
        `/instances/${encodeURIComponent(instance as string)}/series/${seriesId}/season/${seasonNumber}${qs}`,
      );
    },
    enabled: ready && enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
