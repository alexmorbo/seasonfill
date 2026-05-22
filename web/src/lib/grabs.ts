import { useInfiniteQuery } from '@tanstack/react-query';
import { ApiError, api } from './api';
import { useInstanceFilter } from './instance-filter-context-internal';
import type { components } from '@/api/schema';

export type Grab = components['schemas']['dto.Grab'];
export type GrabList = components['schemas']['dto.GrabList'];
export type GrabFilters = { status?: string };

export function useGrabs(filters: GrabFilters = {}) {
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
      if (pageParam) sp.set('cursor', pageParam);
      const qs = sp.toString();
      return api<GrabList>(qs ? `/grabs?${qs}` : '/grabs');
    },
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

export function flattenGrabs(pages: readonly GrabList[] | undefined): readonly Grab[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
