import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

export interface CounterBucketDTO { date: string; grabs: number; imports: number; fails: number; }
export interface CounterTotals { grabs: number; imports: number; fails: number; }
export interface InstanceCountersDTO {
  instance_name: string;
  window: '24h' | '7d' | '30d';
  totals: CounterTotals;
  sparkline: CounterBucketDTO[];
  avg_grabs_7d: number;
}
export interface CountersAggregateDTO { items: InstanceCountersDTO[]; }
export type CounterWindow = '24h' | '7d' | '30d';

export function useCountersAggregate(window: CounterWindow): UseQueryResult<CountersAggregateDTO, ApiError> {
  return useQuery<CountersAggregateDTO, ApiError>({
    queryKey: ['counters', window] as const,
    queryFn: () => api<CountersAggregateDTO>(`/counters?window=${window}`),
    staleTime: 60_000, refetchInterval: 60_000,
    refetchOnWindowFocus: false, placeholderData: keepPreviousData,
  });
}

export function sumTotals(agg?: CountersAggregateDTO): CounterTotals {
  if (!agg) return { grabs: 0, imports: 0, fails: 0 };
  return agg.items.reduce<CounterTotals>(
    (a, it) => ({ grabs: a.grabs + it.totals.grabs, imports: a.imports + it.totals.imports, fails: a.fails + it.totals.fails }),
    { grabs: 0, imports: 0, fails: 0 },
  );
}

export function sumAvgGrabs7d(agg?: CountersAggregateDTO): number {
  if (!agg) return 0;
  return agg.items.reduce((a, it) => a + it.avg_grabs_7d, 0);
}

// rollupDailyGrabs flattens the 7d aggregate into 7 day-buckets summed
// across instances. Always returns a 7-element array (right-pads with
// zeros) so the Sparkline always renders 7 bars.
export function rollupDailyGrabs(agg?: CountersAggregateDTO): number[] {
  const out = [0, 0, 0, 0, 0, 0, 0];
  if (!agg) return out;
  for (const inst of agg.items) {
    inst.sparkline.slice(0, 7).forEach((b, i) => { out[i] = (out[i] ?? 0) + b.grabs; });
  }
  return out;
}
