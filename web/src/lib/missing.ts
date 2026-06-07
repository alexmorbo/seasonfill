import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from './api';
import type { components } from '@/api/schema';

export type MissingSeries = components['schemas']['dto.MissingSeries'];
export type MissingSeasonStat = components['schemas']['dto.MissingSeasonStat'];
export type MissingSeriesList = components['schemas']['dto.MissingSeriesList'];

// Q-010-2: 60s polling. Queue is a working surface, not a status
// dashboard — sub-minute freshness isn't required, and each refetch
// costs one Sonarr ListSeries call upstream.
const POLL_MS = 60_000;

export function useMissing(
  name: string | undefined,
): UseQueryResult<MissingSeriesList, ApiError> {
  return useQuery<MissingSeriesList, ApiError>({
    queryKey: ['missing', name] as const,
    queryFn: () => api<MissingSeriesList>(`/instances/${name}/missing`),
    enabled: Boolean(name),
    staleTime: 30_000,
    refetchInterval: POLL_MS,
    refetchOnWindowFocus: false,
  });
}

export type QueueSort = 'debt' | 'title' | 'year';

// Pure selector: filter by title substring (case-insensitive) and
// sort by debt/title/year. The list is bounded (≤500 in production)
// so we sort in place per render — no memo needed. Empty query
// returns the input order. `year` sorts undefined-last.
export function selectQueueRows(
  items: readonly MissingSeries[],
  q: string,
  sort: QueueSort,
): readonly MissingSeries[] {
  const needle = q.trim().toLowerCase();
  const filtered = needle.length === 0
    ? items
    : items.filter((s) => (s.title ?? '').toLowerCase().includes(needle));
  const sorted = [...filtered];
  switch (sort) {
    case 'title':
      sorted.sort((a, b) =>
        (a.title ?? '').localeCompare(b.title ?? '', undefined, { sensitivity: 'base' }),
      );
      break;
    case 'year':
      sorted.sort((a, b) => {
        const ya = a.year ?? -Infinity;
        const yb = b.year ?? -Infinity;
        return yb - ya;
      });
      break;
    case 'debt':
    default:
      sorted.sort((a, b) => (b.total_missing_aired ?? 0) - (a.total_missing_aired ?? 0));
      break;
  }
  return sorted;
}
