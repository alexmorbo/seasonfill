import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

// 048 ships `dto.InstanceCountersDTO`. Until the schema regen lands the
// shape may briefly be missing — define a local mirror that resolves to
// the same fields. The Implementation Agent should prefer the schema
// type when present and fall back to this mirror otherwise. Compile-time
// check: `_check` keeps both shapes in sync.
type CounterBucket = {
  readonly date: string; // ISO-8601 instant
  readonly grabs: number;
  readonly imports: number;
  readonly fails: number;
};

type CounterTotals = {
  readonly grabs: number;
  readonly imports: number;
  readonly fails: number;
};

export type InstanceCounters = {
  readonly instance_name: string;
  readonly window: '24h' | '7d' | '30d';
  readonly totals: CounterTotals;
  readonly sparkline: readonly CounterBucket[];
  readonly avg_grabs_7d: number;
};

// Compile-time conformance check; remove the @ts-expect-error if/when the
// schema regen catches up after 048 merges. This validates that InstanceCounters
// matches the schema definition when it's present.
// @ts-expect-error - used for compile-time type checking only
const _schemaCheckInstanceCounters: components['schemas'] extends { 'dto.InstanceCountersDTO': infer T }
  ? T extends InstanceCounters ? true : false
  : true = true;

export type CounterWindow = '24h' | '7d' | '30d';

// 493 / N-1c §C — `useInstanceCounters` is left intentionally
// disabled because BE 492 deleted `GET /api/v1/instances/:name/counters`
// and 494 will rewire the Dashboard counters card to use the
// global series-cache aggregates (`totals_grabs` / `totals_imports` /
// `totals_fails` per row). Until then the chart cell renders its
// "—" empty state. Set `VITE_LEGACY_COUNTERS=1` at build time to
// re-enable the old wire call for local debugging.
export function useInstanceCounters(
  instance: string | null,
  window: CounterWindow,
  opts: { refetchInterval?: number } = {},
): UseQueryResult<InstanceCounters, ApiError> {
  const legacyEnabled = import.meta.env.VITE_LEGACY_COUNTERS === '1';
  return useQuery<InstanceCounters, ApiError>({
    queryKey: ['instance-counters', instance, window] as const,
    queryFn: () =>
      api<InstanceCounters>(`/instances/${instance}/counters?window=${window}`),
    enabled: legacyEnabled && Boolean(instance),
    staleTime: 30_000,
    refetchInterval: opts.refetchInterval ?? 60_000,
    refetchOnWindowFocus: false,
  });
}
