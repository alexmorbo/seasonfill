import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Local mirror of 045b dto.SeriesCacheItem / dto.SeriesCacheList.
// Swap to codegen types from @/api/schema once 045b is merged.
export interface SeriesCacheItem {
  readonly sonarr_series_id: number;
  readonly instance_name: string;
  // B-42a: canonical seasonfill series PK from series_cache → series
  // JOIN. Used to navigate to /series/:id (Story 495 / N-1e). Absent
  // on pre-cutover broken rows; the tile falls back to the legacy
  // 3-segment URL in that case.
  readonly series_id?: number;
  readonly title: string;
  readonly title_slug: string;
  readonly year?: number;
  readonly network?: string;
  readonly status?: string;
  // Content-addressed sha256 hex of the stored hero poster. Story 348a
  // backend exposes this via the series_cache enrichment join; Story
  // 349a uses it with mediaUrl() to call /api/v1/media/<hash>. Story 350
  // dropped the legacy poster_path companion.
  readonly poster_hash?: string;
  // Canon TMDB vote_average / vote_count (series.tmdb_rating /
  // series.tmdb_votes), surfaced on the cache row so the unified
  // SeriesCard can render ★rating on the list. Absent when the canon
  // row has no TMDB enrichment yet. Mirrors dto.SeriesCacheItem.
  readonly tmdb_rating?: number;
  readonly tmdb_votes?: number;
  readonly monitored: boolean;
  readonly missing_count: number;
  readonly last_grab_at?: string;
  readonly last_imported_episode?: string;
  readonly last_aired_at?: string;
  readonly updated_at: string;
}

export interface SeriesCacheList {
  readonly items: readonly SeriesCacheItem[];
  readonly total: number;
  readonly has_more: boolean;
  readonly next_cursor?: string;
}

export type SeriesCacheStatus = 'all' | 'imported' | 'missing';
export type SeriesCacheSort = 'updated_desc' | 'title_asc' | 'air_date_desc';

export interface SeriesCacheQuery {
  readonly status?: SeriesCacheStatus;
  readonly limit?: number;
  readonly sort?: SeriesCacheSort;
  // Story E-1-B7 / 584b: raw BCP-47 tag forwarded as `?lang=` so the
  // global catalog list emits localised series titles + per-language
  // posters. Pass-through verbatim (no lowercasing / region-strip).
  // Flows into the queryKey via the `q` spread in useSeriesCache's
  // key below. Empty / undefined omits it.
  readonly lang?: string;
}

// Exported so the lang emit / omit branches can be unit-tested directly
// (otherwise module-private). Pure helper — safe to export.
export function buildPath(instance: string, q: SeriesCacheQuery): string {
  const p = new URLSearchParams();
  p.set('instance', instance);
  // BE 492 global /series accepts `state` for missing/imported/all
  // (see /series-cache → /series rewrite). Keep `status` name on the
  // FE typed query for API stability, map it to wire `state`.
  if (q.status) p.set('state', q.status);
  if (q.limit !== undefined) p.set('limit', String(q.limit));
  if (q.sort) p.set('sort', q.sort);
  if (q.lang) p.set('lang', q.lang);
  return `/series?${p.toString()}`;
}

export function useSeriesCache(
  instance: string | null | undefined,
  q: SeriesCacheQuery,
  opts: { enabled?: boolean } = {},
): UseQueryResult<SeriesCacheList, ApiError> {
  const enabled = (opts.enabled ?? true) && !!instance;
  return useQuery<SeriesCacheList, ApiError>({
    queryKey: ['series-cache', instance ?? '', q] as const,
    queryFn: () => api<SeriesCacheList>(buildPath(instance ?? '', q)),
    enabled,
    staleTime: 60_000,
    refetchInterval: enabled ? 60_000 : false,
    refetchOnWindowFocus: false,
    placeholderData: keepPreviousData,
  });
}

// === 059a additions for /series infinite list ===

import { useInfiniteQuery, type UseInfiniteQueryResult } from '@tanstack/react-query';

// Extend SeriesCacheQuery to accept a cursor for keyset pagination.
// useSeriesCache (above) ignores `cursor` — only useSeriesCacheInfinite
// reads it via `pageParam`.
export interface SeriesCacheInfiniteQuery {
  readonly state?: SeriesCacheStatus;
  readonly sort?: SeriesCacheSort;
  readonly limit?: number;
  // Story 120: case-insensitive substring server-side over title /
  // title_slug. Empty / undefined ⇒ no filter. The repo edge
  // trims the value and escapes LIKE wildcards.
  readonly search?: string;
  // Story 121a §A: tri-state monitored predicate. undefined ⇒ no
  // filter. true ⇒ monitored only. false ⇒ unmonitored only.
  readonly monitoredOnly?: boolean;
  // Story 121a §A: set of broadcast network names. Empty / undefined
  // ⇒ no filter. Pipe-separated server-side; we sort here so the
  // queryKey is stable across reorderings.
  readonly networks?: readonly string[];
  // Story E-1-B7: raw BCP-47 tag forwarded as `?lang=` so the global
  // catalog list emits localised series titles. Pass-through verbatim
  // (no lowercasing / region-strip). Flows into the queryKey via the
  // `q` spread in seriesCacheInfiniteKey. Empty / undefined omits it.
  readonly lang?: string;
}

function buildInfinitePath(
  instance: string,
  q: SeriesCacheInfiniteQuery,
  cursor: string,
): string {
  const p = new URLSearchParams();
  p.set('instance', instance);
  if (q.state) p.set('state', q.state);
  if (q.sort) p.set('sort', q.sort);
  if (q.limit !== undefined) p.set('limit', String(q.limit));
  if (q.search && q.search.trim() !== '') p.set('q', q.search.trim());
  if (q.monitoredOnly !== undefined) {
    p.set('monitored', q.monitoredOnly ? '1' : '0');
  }
  if (q.networks && q.networks.length > 0) {
    p.set('networks', [...q.networks].sort().join('|'));
  }
  if (q.lang) p.set('lang', q.lang);
  if (cursor) p.set('cursor', cursor);
  return `/series?${p.toString()}`;
}

// seriesCacheInfiniteKey already passes the entire `q` into the key
// — adding fields auto-propagates. We add a deterministic networks
// sort in the key as well, otherwise toggling network order would
// blow the cache.
export const seriesCacheInfiniteKey = (
  instance: string | null | undefined,
  q: SeriesCacheInfiniteQuery,
) => {
  const normalized: SeriesCacheInfiniteQuery = q.networks
    ? { ...q, networks: [...q.networks].sort() }
    : q;
  return ['series-cache', 'infinite', instance ?? '', normalized] as const;
};

export function useSeriesCacheInfinite(
  instance: string | null | undefined,
  q: SeriesCacheInfiniteQuery,
  opts: { enabled?: boolean } = {},
): UseInfiniteQueryResult<{ pages: SeriesCacheList[]; pageParams: string[] }, ApiError> {
  const enabled = (opts.enabled ?? true) && !!instance;
  return useInfiniteQuery<
    SeriesCacheList,
    ApiError,
    { pages: SeriesCacheList[]; pageParams: string[] },
    ReturnType<typeof seriesCacheInfiniteKey>,
    string
  >({
    queryKey: seriesCacheInfiniteKey(instance, q),
    queryFn: ({ pageParam }) =>
      api<SeriesCacheList>(buildInfinitePath(instance ?? '', q, pageParam)),
    initialPageParam: '',
    getNextPageParam: (last) => (last.has_more ? last.next_cursor ?? undefined : undefined),
    enabled,
    staleTime: 30_000,
    refetchInterval: enabled ? 60_000 : false,
    refetchOnWindowFocus: false,
  });
}

export function flattenSeriesCachePages(
  pages: readonly SeriesCacheList[] | undefined,
): readonly SeriesCacheItem[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}

// === Story 121a §A — facet networks endpoint ===

interface SeriesCacheNetworksResponse {
  readonly networks: readonly string[];
}

export function useSeriesCacheNetworks(
  instance: string | null | undefined,
  opts: { enabled?: boolean } = {},
): UseQueryResult<readonly string[], ApiError> {
  const enabled = (opts.enabled ?? true) && !!instance;
  return useQuery<readonly string[], ApiError>({
    queryKey: ['series-cache', 'networks', instance ?? ''] as const,
    queryFn: async () => {
      const res = await api<SeriesCacheNetworksResponse>(
        `/series/networks?instance=${encodeURIComponent(instance ?? '')}`,
      );
      return res.networks;
    },
    enabled,
    // Distinct network list is stable; the underlying series_cache
    // doesn't churn fast enough to need a tight refetch interval.
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });
}
