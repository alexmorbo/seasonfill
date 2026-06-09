import { useInfiniteQuery, useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import { useInstanceFilter } from './instance-filter-context-internal';
import type { components } from '@/api/schema';

export type Scan = components['schemas']['dto.Scan'];
export type ScanList = components['schemas']['dto.ScanList'];

// `trigger` is client-side until B9 (see 055 parent story
// §"Backend follow-ups"). `from`/`to` ride the existing wire params.
export type ScanFilters = {
  trigger?: string;
  status?: string;
  from?: string;
  to?: string;
};

function buildQuery(instance: string | null, filters: ScanFilters, cursor: string): string {
  const sp = new URLSearchParams();
  if (instance) sp.set('instance', instance);
  if (filters.status) sp.set('status', filters.status);
  if (filters.from) sp.set('from', filters.from);
  if (filters.to) sp.set('to', filters.to);
  // NOTE: filters.trigger is intentionally omitted — applied
  // client-side by the caller after `flattenScans`. See B9.
  if (cursor) sp.set('cursor', cursor);
  const qs = sp.toString();
  return qs ? `/scans?${qs}` : '/scans';
}

export function useScans(filters: ScanFilters = {}) {
  const { filter: instance } = useInstanceFilter();
  return useInfiniteQuery<
    ScanList,
    ApiError,
    { pages: ScanList[]; pageParams: string[] },
    readonly unknown[],
    string
  >({
    queryKey: ['scans', instance, filters] as const,
    queryFn: ({ pageParam }) => api<ScanList>(buildQuery(instance, filters, pageParam)),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
  });
}

export function useScan(id: string | undefined): UseQueryResult<Scan, ApiError> {
  return useQuery<Scan, ApiError>({
    queryKey: ['scan', id] as const,
    queryFn: () => api<Scan>(`/scans/${id}`),
    enabled: Boolean(id),
    staleTime: 1_000,
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 2_000 : false),
    refetchOnWindowFocus: false,
  });
}

/**
 * useInstanceLatestScan polls /scans?instance=<name> and returns the most
 * recent run for the given instance. While the latest run is `running`
 * it auto-refetches every 6 s so the UI can observe `running → completed`
 * transitions; otherwise polling is disabled.
 *
 * Used by the Force-scan button on InstanceHero (operator pain point #4):
 * lets the button stay in a busy state for the duration of an in-flight
 * scan instead of toggling back to idle immediately after the 202.
 */
export function useInstanceLatestScan(
  instance: string | undefined,
): UseQueryResult<Scan | null, ApiError> {
  return useQuery<Scan | null, ApiError>({
    queryKey: ['scans', 'latest', instance ?? ''] as const,
    queryFn: async () => {
      const sp = new URLSearchParams();
      if (instance) sp.set('instance', instance);
      const qs = sp.toString();
      const list = await api<ScanList>(qs ? `/scans?${qs}` : '/scans');
      // TanStack disallows `undefined` query data — normalise to null.
      return list.items?.[0] ?? null;
    },
    enabled: Boolean(instance),
    staleTime: 0,
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 6_000 : false),
    refetchOnWindowFocus: false,
  });
}

export function flattenScans(pages: readonly ScanList[] | undefined): readonly Scan[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}

// Client-side trigger filter (B9). Pure function, table-tested.
// If `trigger` is undefined/empty/'all', returns the input as-is.
export function filterByTrigger(scans: readonly Scan[], trigger: string | undefined): readonly Scan[] {
  if (!trigger || trigger === 'all') return scans;
  return scans.filter((s) => s.trigger === trigger);
}
