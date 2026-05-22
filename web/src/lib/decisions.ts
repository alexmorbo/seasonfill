import { useInfiniteQuery } from '@tanstack/react-query';
import { ApiError, api } from './api';
import { useInstanceFilter } from './instance-filter-context-internal';
import type { components } from '@/api/schema';

export type Decision = components['schemas']['dto.Decision'];
export type DecisionList = components['schemas']['dto.DecisionList'];
export type DecisionFilters = { decision?: string; scan_run_id?: string };

function buildQuery(instance: string | null, f: DecisionFilters, cursor: string): string {
  const sp = new URLSearchParams();
  if (instance) sp.set('instance', instance);
  if (f.decision) sp.set('decision', f.decision);
  if (f.scan_run_id) sp.set('scan_run_id', f.scan_run_id);
  if (cursor) sp.set('cursor', cursor);
  const qs = sp.toString();
  return qs ? `/decisions?${qs}` : '/decisions';
}

export function useDecisions(filters: DecisionFilters = {}) {
  const { filter: instance } = useInstanceFilter();
  return useInfiniteQuery<
    DecisionList,
    ApiError,
    { pages: DecisionList[]; pageParams: string[] },
    readonly unknown[],
    string
  >({
    queryKey: ['decisions', instance, filters] as const,
    queryFn: ({ pageParam }) => api<DecisionList>(buildQuery(instance, filters, pageParam)),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: 30_000,
    refetchOnWindowFocus: false,
  });
}

export function flattenDecisions(pages: readonly DecisionList[] | undefined): readonly Decision[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
