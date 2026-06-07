import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import type { components } from '@/api/schema';
import {
  windowToDates,
  type DecisionsWindow,
  type Decision,
} from '@/lib/api/decisions';

type DecisionList = components['schemas']['dto.DecisionList'];

export interface UseDecisionsSeasonOptions {
  readonly seriesId: number | null;
  readonly seasonNumber: number | null;
  readonly window: DecisionsWindow;
  readonly enabled?: boolean;
}

export interface DecisionsSeasonResult {
  readonly rows: readonly Decision[];
  readonly grabCount: number;
  readonly cooldownCount: number;
}

function buildQuery(
  instance: string | null,
  seriesId: number,
  season: number,
  window: DecisionsWindow,
): string {
  const sp = new URLSearchParams();
  if (instance) sp.set('instance', instance);
  sp.set('series_id', String(seriesId));
  sp.set('season_number', String(season));
  const { from, to } = windowToDates(window);
  if (from) sp.set('from', from);
  if (to) sp.set('to', to);
  sp.set('limit', '100');
  return `/decisions?${sp.toString()}`;
}

export const decisionsSeasonKey = (
  instance: string | null,
  seriesId: number | null,
  seasonNumber: number | null,
  window: DecisionsWindow,
) => ['decisions', 'season', instance, seriesId, seasonNumber, window] as const;

export function useDecisionsSeason({
  seriesId, seasonNumber, window, enabled = true,
}: UseDecisionsSeasonOptions): UseQueryResult<DecisionsSeasonResult, ApiError> {
  const { filter: instance } = useInstanceFilter();
  const gated = enabled && seriesId !== null && seasonNumber !== null;
  return useQuery<DecisionList, ApiError, DecisionsSeasonResult>({
    queryKey: decisionsSeasonKey(instance, seriesId, seasonNumber, window),
    queryFn: () => api<DecisionList>(buildQuery(instance, seriesId!, seasonNumber!, window)),
    enabled: gated,
    refetchInterval: gated ? 60_000 : false,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    select: (data) => {
      const rows = (data.items ?? []).slice().sort((a, b) => {
        const ta = new Date(a.created_at ?? '').getTime();
        const tb = new Date(b.created_at ?? '').getTime();
        return tb - ta;
      });
      let grabCount = 0;
      let cooldownCount = 0;
      for (const d of rows) {
        if (d.decision === 'grab') grabCount += 1;
        if (d.decision === 'blocked_cooldown') cooldownCount += 1;
      }
      return { rows, grabCount, cooldownCount };
    },
  });
}
