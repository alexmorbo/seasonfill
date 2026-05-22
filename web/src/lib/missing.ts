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
