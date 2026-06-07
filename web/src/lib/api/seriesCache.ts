import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Local mirror of 045b dto.SeriesCacheItem / dto.SeriesCacheList.
// Swap to codegen types from @/api/schema once 045b is merged.
export interface SeriesCacheItem {
  readonly sonarr_series_id: number;
  readonly instance_name: string;
  readonly title: string;
  readonly title_slug: string;
  readonly year?: number;
  readonly network?: string;
  readonly status?: string;
  readonly poster_path?: string;
  readonly monitored: boolean;
  readonly missing_count: number;
  readonly last_grab_at?: string;
  readonly last_imported_episode?: string;
  readonly updated_at: string;
}

export interface SeriesCacheList {
  readonly items: readonly SeriesCacheItem[];
  readonly total: number;
  readonly has_more: boolean;
  readonly next_cursor?: string;
}

export type SeriesCacheStatus = 'all' | 'imported' | 'missing';
export type SeriesCacheSort = 'updated_desc' | 'title_asc';

export interface SeriesCacheQuery {
  readonly status?: SeriesCacheStatus;
  readonly limit?: number;
  readonly sort?: SeriesCacheSort;
}

function buildPath(instance: string, q: SeriesCacheQuery): string {
  const p = new URLSearchParams();
  if (q.status) p.set('status', q.status);
  if (q.limit !== undefined) p.set('limit', String(q.limit));
  if (q.sort) p.set('sort', q.sort);
  const qs = p.toString();
  return `/instances/${encodeURIComponent(instance)}/series-cache${qs ? `?${qs}` : ''}`;
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
