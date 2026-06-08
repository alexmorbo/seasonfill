import { useQuery } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

type DecisionList = components['schemas']['dto.DecisionList'];

export interface SourceDecisionKey {
  readonly instance: string | null;
  readonly scanRunID: string | null;
  readonly seriesID: number | null;
  readonly seasonNumber: number | null;
}

export const sourceDecisionQueryKey = (k: SourceDecisionKey) =>
  ['grabs', 'source-decision', k.instance, k.scanRunID, k.seriesID, k.seasonNumber] as const;

export function useSourceDecisionID(k: SourceDecisionKey): string | null {
  const enabled =
    k.scanRunID !== null && k.seriesID !== null && k.seasonNumber !== null;
  const q = useQuery<DecisionList, ApiError, string | null>({
    queryKey: sourceDecisionQueryKey(k),
    queryFn: () => {
      const sp = new URLSearchParams();
      if (k.instance) sp.set('instance', k.instance);
      sp.set('scan_run_id', k.scanRunID!);
      sp.set('series_id', String(k.seriesID!));
      sp.set('season_number', String(k.seasonNumber!));
      sp.set('limit', '1');
      return api<DecisionList>(`/decisions?${sp.toString()}`);
    },
    enabled,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
    select: (data) => data.items?.[0]?.id ?? null,
  });
  return q.data ?? null;
}
