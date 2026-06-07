import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Inline mirror of dto.WatchdogRollup (story 047a L651-669). Only the
// F1-consumed fields are required.
export interface WatchdogRollup {
  instance_name: string;
  enabled: boolean;
  active: boolean;
  watched: number;
  unregistered?: number;
  regrabs_24h?: number;
  regrabs_7d: number;
  blacklist_size: number;
  last_poll_at?: string;
  last_poll_result?: string;
  next_poll_at?: string;
  qbit_reachable: boolean;
  poll_interval_seconds?: number;
  cooldown_hours?: number;
  no_better_max?: number;
}
export interface WatchdogRollupList { items: WatchdogRollup[]; }

export function useWatchdogRollups(): UseQueryResult<WatchdogRollupList, ApiError> {
  return useQuery<WatchdogRollupList, ApiError>({
    queryKey: ['watchdog-rollups'] as const,
    queryFn: () => api<WatchdogRollupList>('/watchdog/rollups'),
    staleTime: 60_000, refetchInterval: 60_000,
    refetchOnWindowFocus: false, placeholderData: keepPreviousData,
  });
}

// Status chip priority: unreachable (any enabled && !qbit_reachable) >
// running (any enabled && qbit_reachable) > off (nothing enabled).
export type WatchdogChip = 'running' | 'off' | 'unreachable';
export function rollupChipStatus(list?: WatchdogRollupList): WatchdogChip {
  if (!list || list.items.length === 0) return 'off';
  if (list.items.some((r) => r.enabled && !r.qbit_reachable)) return 'unreachable';
  return list.items.some((r) => r.enabled && r.qbit_reachable) ? 'running' : 'off';
}

export interface RollupTotals { watched: number; regrabs_7d: number; blacklist_size: number; }
export function sumRollupTotals(list?: WatchdogRollupList): RollupTotals {
  if (!list) return { watched: 0, regrabs_7d: 0, blacklist_size: 0 };
  return list.items.reduce<RollupTotals>(
    (a, r) => ({
      watched: a.watched + r.watched,
      regrabs_7d: a.regrabs_7d + r.regrabs_7d,
      blacklist_size: a.blacklist_size + r.blacklist_size,
    }),
    { watched: 0, regrabs_7d: 0, blacklist_size: 0 },
  );
}
