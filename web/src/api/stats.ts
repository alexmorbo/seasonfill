import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// I-4 wire types — GET /api/v1/insights/stats?instance= → dto.StatsReportDTO.
// Every field is optional in the generated schema; the FE treats a missing
// number as 0 and a missing array as [].
export type StatsReport = components['schemas']['dto.StatsReportDTO'];
export type StatsInstance = components['schemas']['dto.StatsInstanceDTO'];
export type StatsTotals = components['schemas']['dto.StatsTotalsDTO'];
export type StatsKind = components['schemas']['dto.StatsKindDTO'];
export type StatsGrabSuccess = components['schemas']['dto.StatsGrabSuccessDTO'];
export type StatsTorrentTotals = components['schemas']['dto.StatsTorrentTotalsDTO'];

// useStats fetches the library statistics report ON DEMAND. Manual-refresh
// page (no refetchInterval); staleTime keeps a page bounce from re-running
// the aggregation. When `instance` is provided the BE scopes the report to
// that one instance; otherwise every instance is returned.
export function useStats(instance?: string): UseQueryResult<StatsReport, ApiError> {
  return useQuery<StatsReport, ApiError>({
    queryKey: ['insights', 'stats', instance ?? 'all'] as const,
    queryFn: () =>
      api<StatsReport>(
        '/insights/stats' + (instance ? `?instance=${encodeURIComponent(instance)}` : ''),
      ),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
