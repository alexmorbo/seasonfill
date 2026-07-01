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
  // Story E-1-B7 — raw BCP-47 tag forwarded as `?lang=` so the BE
  // emits localised series titles. Pass-through verbatim (no
  // lowercasing / region-strip). Empty string omits the param.
  lang: string = '',
): string {
  const sp = new URLSearchParams();
  if (filters.instance) sp.set('instance', filters.instance);
  if (filters.q.trim()) sp.set('q', filters.q.trim());
  if (filters.cooldownOnly) sp.set('cooldown_only', 'true');
  if (filters.blacklistedOnly) sp.set('blacklisted_only', 'true');
  if (cursor) sp.set('cursor', cursor);
  sp.set('limit', String(limit));
  if (lang) sp.set('lang', lang);
  return `/watchdog/seasons?${sp.toString()}`;
}

// Story E-1-B7 — `lang` is part of the key so switching language
// refetches localised titles instead of serving en-US from cache.
export const watchdogSeasonsKey = (
  filters: WatchdogSeasonsFilters,
  lang: string = '',
) =>
  [
    'watchdog',
    'seasons',
    filters.instance,
    filters.q.trim(),
    filters.cooldownOnly,
    filters.blacklistedOnly,
    lang,
  ] as const;

export function useWatchdogSeasons(
  filters: WatchdogSeasonsFilters,
  limit: number = DEFAULT_LIMIT,
  lang: string = '',
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
    queryKey: watchdogSeasonsKey(filters, lang),
    queryFn: ({ pageParam }) =>
      api<WatchdogSeasonsList>(buildSeasonsQuery(filters, pageParam, limit, lang)),
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

// === series drill-down (Story 098c) ==============================
// Endpoint B from Story 098a returns the per-season aggregation for
// one series at one instance: origin, stats, cooldown, blacklist,
// no-better counter, plus the most recent decisions and grabs. The
// drawer mounts whenever both args are non-null.

export const watchdogSeriesDetailKey = (
  instance: string | null,
  seriesID: number | null,
  lang: string = '',
) => ['watchdog', 'series', instance, seriesID, lang] as const;

export function useWatchdogSeriesDetail(
  instance: string | null,
  seriesID: number | null,
  lang: string = '',
): UseQueryResult<WatchdogSeriesDetail, ApiError> {
  const enabled = Boolean(instance) && seriesID !== null && Number.isFinite(seriesID);
  return useQuery<WatchdogSeriesDetail, ApiError>({
    queryKey: watchdogSeriesDetailKey(instance, seriesID, lang),
    queryFn: () => {
      // Story E-1-B7 — raw BCP-47 `?lang=` so the per-series drill
      // renders a localised title. Empty string omits the param.
      const langQs = lang ? `?lang=${encodeURIComponent(lang)}` : '';
      return api<WatchdogSeriesDetail>(
        `/watchdog/series/${encodeURIComponent(instance!)}/${seriesID!}${langQs}`,
      );
    },
    enabled,
    refetchInterval: enabled ? 60_000 : false,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
