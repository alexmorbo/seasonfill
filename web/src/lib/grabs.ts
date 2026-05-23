import { useInfiniteQuery } from '@tanstack/react-query';
import { ApiError, api } from './api';
import { useInstanceFilter } from './instance-filter-context-internal';
import type { components } from '@/api/schema';

export type Grab = components['schemas']['dto.Grab'];
export type GrabList = components['schemas']['dto.GrabList'];
export type GrabFilters = {
  status?: string;
  scan_run_id?: string;
  series_id?: number;
};

export interface UseGrabsOptions {
  // Switch to 2s polling cadence while a scan is in-flight. Off the
  // queryKey so cache hits survive the running→completed transition.
  readonly fastPoll?: boolean;
}

export function useGrabs(filters: GrabFilters = {}, opts: UseGrabsOptions = {}) {
  const { filter: instance } = useInstanceFilter();
  return useInfiniteQuery<
    GrabList,
    ApiError,
    { pages: GrabList[]; pageParams: string[] },
    readonly unknown[],
    string
  >({
    queryKey: ['grabs', instance, filters] as const,
    queryFn: ({ pageParam }) => {
      const sp = new URLSearchParams();
      if (instance) sp.set('instance', instance);
      if (filters.status) sp.set('status', filters.status);
      if (filters.scan_run_id) sp.set('scan_run_id', filters.scan_run_id);
      if (filters.series_id !== undefined) sp.set('series_id', String(filters.series_id));
      if (pageParam) sp.set('cursor', pageParam);
      const qs = sp.toString();
      return api<GrabList>(qs ? `/grabs?${qs}` : '/grabs');
    },
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    staleTime: 30_000,
    refetchInterval: opts.fastPoll ? 2_000 : false,
    refetchOnWindowFocus: false,
  });
}

export function flattenGrabs(pages: readonly GrabList[] | undefined): readonly Grab[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
