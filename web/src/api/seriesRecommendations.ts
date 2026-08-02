import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';
import {
  useDegradedPollInterval,
  type DegradedPollConfig,
} from '@/hooks/useDegradedPollInterval';
// Re-export the visibility composer so RecommendationsCarousel can
// gate fetches behind viewport-intersection without a second import.
export { useIsSectionVisible } from '@/api/seriesTorrents';

export type SeriesRecommendationsResponse = components['schemas']['dto.SeriesRecommendationsResponse'];
export type Recommendation = components['schemas']['dto.Recommendation'];

export interface UseSeriesRecommendationsParams {
  readonly seriesId: number | undefined;
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
  // Story 565 (B-recs-lang) — BCP-47 tag forwarded as `?lang=` so the
  // BE emits localised recommendation titles. Included in the queryKey
  // so TanStack isolates cache per language (else en-US bleeds into
  // ru-RU on locale switch). Empty string omits the URL param.
  readonly lang?: string | undefined;
  // Page-level gate (caller passes the intersection-observer result).
  // When false the query is disabled — no key, no fetch.
  readonly enabled?: boolean | undefined;
  // Same degraded-poll behaviour as useSeriesOverview.
  readonly pollWhileDegraded?: boolean | undefined;
}

const DEFAULT_LIMIT = 20;
const DEFAULT_OFFSET = 0;

export function seriesRecommendationsQueryKey(
  seriesId: number,
  limit: number,
  offset: number,
  lang: string,
): readonly ['series-recommendations', number, number, number, string] {
  return ['series-recommendations', seriesId, limit, offset, lang] as const;
}

// REC-2: 'media_cold' is the BE degraded tag set while any recommendation
// poster is still cold (missing-art sentinel). Including it keeps
// pollWhileDegraded re-polling until every rec poster warms.
const HOT_SOURCES = new Set<string>(['tmdb_series', 'media_cold']);
export function isHotDegraded(resp: SeriesRecommendationsResponse | undefined): boolean {
  if (!resp || !resp.degraded || resp.degraded.length === 0) return false;
  return resp.degraded.some((s) => HOT_SOURCES.has(s));
}

// HARDEN-1: bound the degraded poll. 6 ticks at 4s ≈ 24s ceiling, mirroring
// useSeries's POLL_MAX_TICKS. 'length-reset' — the counter resets when
// `degraded[].length` changes so a fresh degraded wave re-earns the budget,
// but a stuck media_cold poster (blob never downloads) stops polling instead
// of re-fetching forever. Exported so the cap is unit-testable via
// createDegradedPollInterval.
const RECS_POLL_MS = 4_000;
const RECS_MAX_TICKS = 6;
export function recommendationsPollConfig(
  pollWhileDegraded: boolean,
): DegradedPollConfig<SeriesRecommendationsResponse> {
  return {
    enabled: pollWhileDegraded,
    isDegraded: isHotDegraded,
    intervalFor: () => RECS_POLL_MS,
    maxTicks: RECS_MAX_TICKS,
    mode: 'length-reset',
    degradedLen: (d) => d?.degraded?.length ?? 0,
  };
}

export function useSeriesRecommendations({
  seriesId,
  limit,
  offset,
  lang,
  enabled,
  pollWhileDegraded,
}: UseSeriesRecommendationsParams): UseQueryResult<SeriesRecommendationsResponse> {
  const effectiveLimit = limit ?? DEFAULT_LIMIT;
  const effectiveOffset = offset ?? DEFAULT_OFFSET;
  const effectiveLang = lang ?? '';
  const ready = (enabled ?? true) && typeof seriesId === 'number' && seriesId > 0;
  const refetchInterval = useDegradedPollInterval(
    recommendationsPollConfig(!!pollWhileDegraded),
  );
  return useQuery<SeriesRecommendationsResponse>({
    queryKey: ready
      ? seriesRecommendationsQueryKey(seriesId as number, effectiveLimit, effectiveOffset, effectiveLang)
      : (['series-recommendations', 0, effectiveLimit, effectiveOffset, effectiveLang] as const),
    queryFn: () => {
      const langQs = effectiveLang ? `&lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<SeriesRecommendationsResponse>(
        `/series/${seriesId}/recommendations?limit=${effectiveLimit}&offset=${effectiveOffset}${langQs}`,
      );
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    refetchInterval,
  });
}
