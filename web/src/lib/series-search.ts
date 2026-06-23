import { useQuery, type UseQueryResult, keepPreviousData } from '@tanstack/react-query';
import { ApiError, api } from './api';

// FE-local shapes for the global /series search picker payload.
// Mirrors the deleted BE per-instance dto.SeriesSearch* wire types;
// kept here because the legacy picker still consumes this shape from
// the global endpoint (which projects into the legacy shape on the BE).
export interface SeriesSearchItem {
  readonly series_id?: number;
  readonly title?: string;
  readonly monitored?: boolean;
  readonly season_count?: number;
  readonly missing_aired_count?: number;
}

export interface SeriesSearchList {
  readonly items?: readonly SeriesSearchItem[];
  readonly total?: number;
}

export interface UseSeriesSearchOpts {
  readonly instance: string;
  readonly query: string;
  readonly monitored?: boolean;
  readonly limit?: number;
  readonly enabled?: boolean;
}

// Caller debounces upstream (in <SeriesPicker>) — this hook fires
// on every `query` change. staleTime caches identical queries
// across modal open/close so repeat typing is free.
const STALE_MS = 30_000;

export function useSeriesSearch(
  opts: UseSeriesSearchOpts,
): UseQueryResult<SeriesSearchList, ApiError> {
  const { instance, query, monitored = true, limit = 30 } = opts;
  const enabled = opts.enabled !== false && instance.length > 0;

  return useQuery<SeriesSearchList, ApiError>({
    queryKey: ['series-search', instance, query, monitored, limit] as const,
    queryFn: () => {
      const params = new URLSearchParams();
      params.set('instance', instance);
      if (query) params.set('q', query);
      params.set('monitored', monitored ? 'true' : 'false');
      params.set('limit', String(limit));
      return api<SeriesSearchList>(
        `/series?${params.toString()}`,
      );
    },
    enabled,
    staleTime: STALE_MS,
    // Smooth UX: dropdown doesn't blank between keystrokes.
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
  });
}
