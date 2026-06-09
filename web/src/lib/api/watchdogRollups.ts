import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Wire shape matches `dto.WatchdogRollup` exactly (verified against
// the deployed `/api/v1/watchdog/rollups` payload and the generated
// `components['schemas']['dto.WatchdogRollup']`). Story 090 fixed an
// earlier guess that used `instance`, `poll_interval_min`,
// `regrab_cooldown_h`, `max_no_better` — none of those existed on the
// wire, which caused `?edit=undefined` and NaN chips.
export interface WatchdogRollup {
  instance_name: string;
  enabled: boolean;
  active: boolean;
  watched: number;
  unregistered: number;
  regrabs_24h: number;
  regrabs_7d: number;
  blacklist_size: number;
  last_poll_at?: string | undefined;
  last_poll_result?: 'ok' | 'qbit_error' | 'skipped' | undefined;
  next_poll_at?: string | undefined;
  qbit_reachable: boolean;
  poll_interval_seconds: number;
  cooldown_hours: number;
  no_better_max: number;
}

export interface WatchdogRollupAggregate {
  items: WatchdogRollup[];
}

export const watchdogRollupsKey = () => ['watchdog', 'rollups'] as const;

export function useWatchdogRollups(): UseQueryResult<
  WatchdogRollupAggregate,
  ApiError
> {
  return useQuery<WatchdogRollupAggregate, ApiError>({
    queryKey: watchdogRollupsKey(),
    queryFn: () => api<WatchdogRollupAggregate>('/watchdog/rollups'),
    refetchInterval: 30_000,
    staleTime: 15_000,
    refetchOnWindowFocus: false,
  });
}

// Status chip priority: unreachable (any enabled && !qbit_reachable) >
// running (any enabled && qbit_reachable) > off (nothing enabled).
export type WatchdogChip = 'running' | 'off' | 'unreachable';
export function rollupChipStatus(agg?: WatchdogRollupAggregate): WatchdogChip {
  if (!agg || agg.items.length === 0) return 'off';
  if (agg.items.some((r) => r.enabled && !r.qbit_reachable)) return 'unreachable';
  return agg.items.some((r) => r.enabled && r.qbit_reachable) ? 'running' : 'off';
}

export interface RollupTotals {
  watched: number;
  regrabs_7d: number;
  blacklist_size: number;
}
export function sumRollupTotals(agg?: WatchdogRollupAggregate): RollupTotals {
  if (!agg) return { watched: 0, regrabs_7d: 0, blacklist_size: 0 };
  return agg.items.reduce<RollupTotals>(
    (a, r) => ({
      watched: a.watched + r.watched,
      regrabs_7d: a.regrabs_7d + r.regrabs_7d,
      blacklist_size: a.blacklist_size + r.blacklist_size,
    }),
    { watched: 0, regrabs_7d: 0, blacklist_size: 0 },
  );
}

export function countActiveInstances(agg?: WatchdogRollupAggregate): {
  active: number;
  total: number;
} {
  if (!agg) return { active: 0, total: 0 };
  const total = agg.items.length;
  const active = agg.items.reduce(
    (n, r) => n + (r.enabled && r.qbit_reachable ? 1 : 0),
    0,
  );
  return { active, total };
}
