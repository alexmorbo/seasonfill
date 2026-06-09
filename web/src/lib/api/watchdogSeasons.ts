import {
  useInfiniteQuery,
  useQuery,
  type UseInfiniteQueryResult,
  type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Story 098a backend types. Re-exported for component callers so they
// can stay decoupled from the generated schema module path.
export type WatchdogSeason = components['schemas']['dto.WatchdogSeason'];
export type WatchdogSeasonsList = components['schemas']['dto.WatchdogSeasonsList'];
export type WatchdogSeriesDetail = components['schemas']['dto.WatchdogSeriesDetail'];

export interface WatchdogSeasonsFilters {
  readonly instance: string | null;
  readonly q: string;
  readonly cooldownOnly: boolean;
  readonly blacklistedOnly: boolean;
}

const DEFAULT_LIMIT = 50;

export function buildSeasonsQuery(
  filters: WatchdogSeasonsFilters,
  cursor: string,
  limit: number,
): string {
  const sp = new URLSearchParams();
  if (filters.instance) sp.set('instance', filters.instance);
  if (filters.q.trim()) sp.set('q', filters.q.trim());
  if (filters.cooldownOnly) sp.set('cooldown_only', 'true');
  if (filters.blacklistedOnly) sp.set('blacklisted_only', 'true');
  if (cursor) sp.set('cursor', cursor);
  sp.set('limit', String(limit));
  return `/watchdog/seasons?${sp.toString()}`;
}

export const watchdogSeasonsKey = (filters: WatchdogSeasonsFilters) =>
  [
    'watchdog',
    'seasons',
    filters.instance,
    filters.q.trim(),
    filters.cooldownOnly,
    filters.blacklistedOnly,
  ] as const;

export function useWatchdogSeasons(
  filters: WatchdogSeasonsFilters,
  limit: number = DEFAULT_LIMIT,
): UseInfiniteQueryResult<
  { pages: WatchdogSeasonsList[]; pageParams: string[] },
  ApiError
> {
  return useInfiniteQuery<
    WatchdogSeasonsList,
    ApiError,
    { pages: WatchdogSeasonsList[]; pageParams: string[] },
    readonly unknown[],
    string
  >({
    queryKey: watchdogSeasonsKey(filters),
    queryFn: ({ pageParam }) =>
      api<WatchdogSeasonsList>(buildSeasonsQuery(filters, pageParam, limit)),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 60_000,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

export function flattenSeasons(
  pages: readonly WatchdogSeasonsList[] | undefined,
): readonly WatchdogSeason[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}

// === aggregate totals for the top strip ===========================
// The strip needs two new tiles ("cooldown seasons", "tracked origins")
// derived from the same dataset. We issue one separate read with a
// high limit so the table's filtered/paginated query cache is not
// affected. Counts are recomputed on every poll; if next_cursor is
// non-empty the "+" hint is appended downstream.

export interface WatchdogSeasonsTotals {
  readonly origins: number; // total rows = tracked origins
  readonly cooldownActive: number; // items whose cooldown != null
  readonly truncated: boolean; // true when next_cursor present
}

export function useWatchdogSeasonsTotals(): UseQueryResult<
  WatchdogSeasonsTotals,
  ApiError
> {
  return useQuery<WatchdogSeasonsList, ApiError, WatchdogSeasonsTotals>({
    queryKey: ['watchdog', 'seasons', 'totals'] as const,
    queryFn: () => api<WatchdogSeasonsList>('/watchdog/seasons?limit=500'),
    refetchInterval: 60_000,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    select: (data) => {
      const items = data.items ?? [];
      const cooldownActive = items.reduce(
        (n, r) => n + (r.cooldown ? 1 : 0),
        0,
      );
      return {
        origins: items.length,
        cooldownActive,
        truncated: Boolean(data.next_cursor),
      };
    },
  });
}
