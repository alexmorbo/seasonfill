import {
  useInfiniteQuery,
  useQuery,
  type UseInfiniteQueryResult,
  type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import type { components } from '@/api/schema';
import {
  categoryToBucket,
  type ReasonCategoryKey,
} from '@/lib/decisions/reasonCategory';

export type Decision = components['schemas']['dto.Decision'];
export type DecisionList = components['schemas']['dto.DecisionList'];

export type DecisionsWindow = '24h' | '7d' | '30d' | 'all';
export type DecisionsSort = 'freshest' | 'stuck-first';

export interface DecisionsListFilters {
  readonly category: ReasonCategoryKey | 'all';
  readonly window: DecisionsWindow;
  readonly sort: DecisionsSort;
  readonly search: string; // applies to series_title; case-insensitive
}

const WINDOW_MS: Record<Exclude<DecisionsWindow, 'all'>, number> = {
  '24h': 24 * 3_600_000,
  '7d':  7 * 86_400_000,
  '30d': 30 * 86_400_000,
};

// Computes ISO `from`/`to` bounds for the endpoint. `to` is always
// `now()` so the window slides on each refetch; for `window=all` we
// drop both bounds.
export function windowToDates(window: DecisionsWindow, now: Date = new Date()):
  { from?: string; to?: string } {
  if (window === 'all') return {};
  const span = WINDOW_MS[window];
  return { from: new Date(now.getTime() - span).toISOString(), to: now.toISOString() };
}

export function buildListQuery(
  instance: string | null,
  window: DecisionsWindow,
  cursor: string,
  limit: number,
): string {
  const sp = new URLSearchParams();
  if (instance) sp.set('instance', instance);
  const { from, to } = windowToDates(window);
  if (from) sp.set('from', from);
  if (to) sp.set('to', to);
  if (cursor) sp.set('cursor', cursor);
  sp.set('limit', String(limit));
  return `/decisions?${sp.toString()}`;
}

export const decisionsListKey = (
  instance: string | null,
  window: DecisionsWindow,
) => ['decisions', 'list', instance, window] as const;

export function useDecisionsList(opts: {
  window: DecisionsWindow;
  limit?: number;
}): UseInfiniteQueryResult<{ pages: DecisionList[]; pageParams: string[] }, ApiError> {
  const { filter: instance } = useInstanceFilter();
  const limit = opts.limit ?? 200;
  return useInfiniteQuery<
    DecisionList,
    ApiError,
    { pages: DecisionList[]; pageParams: string[] },
    readonly unknown[],
    string
  >({
    queryKey: decisionsListKey(instance, opts.window),
    queryFn: ({ pageParam }) =>
      api<DecisionList>(buildListQuery(instance, opts.window, pageParam, limit)),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    refetchInterval: 60_000,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

export function flattenDecisionList(
  pages: readonly DecisionList[] | undefined,
): readonly Decision[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}

// Reduce all loaded decisions to the latest per (series_id, season_number).
// Returns rows in input order (which is already created_at DESC from the
// endpoint). Used by both the accordion (053b) and the stuck-detection
// reducer (053a2 adds the latter).
export interface LatestPerSeason {
  readonly decision: Decision;
  readonly count: number; // total decisions for this (series, season) in window
  readonly streakNothing: number; // consecutive `nothing_found` from newest backwards
}

export function reduceLatestPerSeason(
  rows: readonly Decision[],
): ReadonlyMap<string, LatestPerSeason> {
  // Walk rows in chronological order from newest to oldest. The endpoint
  // already returns DESC by created_at, so the first encounter of a
  // (series, season) is the latest. The "consecutive nothing_found"
  // streak walks subsequent same-key decisions until a non-nothing_found
  // breaks the chain.
  const out = new Map<string, { decision: Decision; count: number; streak: number; streakActive: boolean }>();
  for (const d of rows) {
    const key = `${d.series_id ?? -1}:${d.season_number ?? -1}`;
    const slot = out.get(key);
    const isNothing = d.category === 'nothing_found';
    if (slot === undefined) {
      out.set(key, {
        decision: d,
        count: 1,
        streak: isNothing ? 1 : 0,
        streakActive: isNothing,
      });
    } else {
      slot.count += 1;
      if (slot.streakActive && isNothing) slot.streak += 1;
      else slot.streakActive = false;
    }
  }
  const result = new Map<string, LatestPerSeason>();
  for (const [k, v] of out) {
    result.set(k, { decision: v.decision, count: v.count, streakNothing: v.streak });
  }
  return result;
}

// Applies client-side filtering for the F7 Filters bar. Returns the
// rows that should feed the accordion. Sort is applied AFTER filter
// to match the design's expectation that "Сорт. застрявшие сверху"
// reorders the filtered output (not the raw load).
export function applyDecisionsFilters(
  rows: readonly Decision[],
  filters: DecisionsListFilters,
): readonly Decision[] {
  const q = filters.search.trim().toLowerCase();
  const filtered = rows.filter((d) => {
    if (filters.category !== 'all') {
      if (categoryToBucket(d.category) !== filters.category) return false;
    }
    if (q && !(d.series_title ?? '').toLowerCase().includes(q)) return false;
    return true;
  });
  if (filters.sort === 'freshest') return filtered;
  // stuck-first: prioritise rows whose category bucket is `none`,
  // descending by season streak (computed via `reduceLatestPerSeason`
  // here so the comparator stays O(n log n) without extra fetches).
  const latest = reduceLatestPerSeason(filtered);
  return [...filtered].sort((a, b) => {
    const ka = `${a.series_id ?? -1}:${a.season_number ?? -1}`;
    const kb = `${b.series_id ?? -1}:${b.season_number ?? -1}`;
    const sa = latest.get(ka)?.streakNothing ?? 0;
    const sb = latest.get(kb)?.streakNothing ?? 0;
    if (sa !== sb) return sb - sa;
    return new Date(b.created_at ?? '').getTime() - new Date(a.created_at ?? '').getTime();
  });
}

// === stuck seasons (client-side derivation) ===
// Stuck = N≥3 consecutive `nothing_found` decisions on the same
// (series, season) across the chosen window. Sorted by streak DESC.
// Future story B7-stuck-counter will swap this for a server-side
// counter — `useStuckSeasons` is the single read-site to change.

export interface StuckSeason {
  readonly seriesId: number;
  readonly seriesTitle: string;
  readonly seasonNumber: number;
  readonly consecutive: number;
  readonly lastReason: string;
  readonly lastDecisionId: string;
  readonly lastScanRunId: string;
  readonly instance: string;
}

export const stuckKey = (
  instance: string | null,
  window: DecisionsWindow,
) => ['decisions', 'stuck', instance, window] as const;

// Uses the same fetched dataset as `useDecisionsList` via React-Query
// cache sharing — calling both hooks does NOT issue a second network
// request because the response shape is identical and `staleTime=30s`
// merges them on the next polling tick. The `select` derives the
// stuck list in O(n) over the loaded pages.
export function useStuckSeasons(opts: {
  window: DecisionsWindow;
  threshold?: number;
}): UseQueryResult<readonly StuckSeason[], ApiError> {
  const { filter: instance } = useInstanceFilter();
  const limit = 200;
  const threshold = opts.threshold ?? 3;
  return useQuery<DecisionList, ApiError, readonly StuckSeason[]>({
    queryKey: ['decisions', 'list', instance, opts.window, 'stuck-derive', threshold] as const,
    queryFn: () => api<DecisionList>(buildListQuery(instance, opts.window, '', limit)),
    refetchInterval: 60_000,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    select: (data) => {
      const rows = data.items ?? [];
      const latest = reduceLatestPerSeason(rows);
      const out: StuckSeason[] = [];
      for (const v of latest.values()) {
        if (v.streakNothing < threshold) continue;
        const d = v.decision;
        out.push({
          seriesId: d.series_id ?? -1,
          seriesTitle: d.series_title ?? '—',
          seasonNumber: d.season_number ?? -1,
          consecutive: v.streakNothing,
          lastReason: d.reason ?? 'unknown',
          lastDecisionId: d.id ?? '',
          lastScanRunId: d.scan_run_id ?? '',
          instance: d.instance ?? '',
        });
      }
      out.sort((a, b) => b.consecutive - a.consecutive);
      return out;
    },
  });
}
