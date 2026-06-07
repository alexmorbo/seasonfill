// TODO(F6): consumed by ScanDetail.tsx (useDecisions/flattenDecisions
// with scan_run_id + decision filters and fastPoll). F6's redesign will
// migrate that page to the F7 `lib/api/decisions` module. Do not delete
// until ScanDetail has been migrated.
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

export interface UseDecisionsOptions {
  // Switch to 2s polling cadence while a scan is in-flight. Keep off the
  // queryKey so cache hits survive the running→completed transition.
  readonly fastPoll?: boolean;
}

export function useDecisions(filters: DecisionFilters = {}, opts: UseDecisionsOptions = {}) {
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
    refetchInterval: opts.fastPoll ? 2_000 : 30_000,
    refetchOnWindowFocus: false,
  });
}

export function flattenDecisions(pages: readonly DecisionList[] | undefined): readonly Decision[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
