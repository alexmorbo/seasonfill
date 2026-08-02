import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';
import {
  useDegradedPollInterval,
  type DegradedPollConfig,
} from '@/hooks/useDegradedPollInterval';

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

// HARDEN-1: bound the degraded poll. 6 ticks at 4s ≈ 24s ceiling, mirroring
// useSeries's POLL_MAX_TICKS. 'length-reset' — the counter resets whenever
// `degraded[].length` changes, so a fresh degraded wave re-earns the budget
// while a stuck one (poster that never warms) stops polling. Exported so the
// cap is unit-testable without React (createDegradedPollInterval).
const OVERVIEW_POLL_MS = 4_000;
const OVERVIEW_MAX_TICKS = 6;
export function overviewPollConfig(
  pollWhileDegraded: boolean,
): DegradedPollConfig<SeriesOverviewResponse> {
  return {
    enabled: pollWhileDegraded,
    isDegraded: isHotDegraded,
    intervalFor: () => OVERVIEW_POLL_MS,
    maxTicks: OVERVIEW_MAX_TICKS,
    mode: 'length-reset',
    degradedLen: (d) => d?.degraded?.length ?? 0,
  };
}

export function useSeriesOverview({
  seriesId,
  lang,
  pollWhileDegraded,
}: UseSeriesOverviewParams): UseQueryResult<SeriesOverviewResponse> {
  const ready = typeof seriesId === 'number' && seriesId > 0;
  const effectiveLang = lang ?? '';
  const refetchInterval = useDegradedPollInterval(overviewPollConfig(!!pollWhileDegraded));
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
    refetchInterval,
  });
}
