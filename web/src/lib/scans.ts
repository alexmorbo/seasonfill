import { useInfiniteQuery, useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import { useInstanceFilter } from './instance-filter-context-internal';
import type { components } from '@/api/schema';

export type Scan = components['schemas']['dto.Scan'];
export type ScanList = components['schemas']['dto.ScanList'];
export type ScanFilters = { trigger?: string; status?: string };

function buildQuery(instance: string | null, filters: ScanFilters, cursor: string): string {
  const sp = new URLSearchParams();
  if (instance) sp.set('instance', instance);
  if (filters.status) sp.set('status', filters.status);
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
    staleTime: 10_000,
  });
}

export function flattenScans(pages: readonly ScanList[] | undefined): readonly Scan[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
