import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// I-2a wire types — GET /api/v1/insights/gaps?instance= → dto.GapReportDTO.
// Every field is optional in the generated schema; the FE treats a missing
// count as 0 and a missing array as [].
export type GapReport = components['schemas']['dto.GapReportDTO'];
export type GapInstance = components['schemas']['dto.GapInstanceDTO'];
export type GapSeries = components['schemas']['dto.GapSeriesDTO'];
export type GapSeason = components['schemas']['dto.GapSeasonDTO'];
export type GapEpisode = components['schemas']['dto.GapEpisodeDTO'];

// useGaps fetches the library gap report ON DEMAND. No refetchInterval:
// this is a manual-refresh page, not a live monitor. staleTime keeps a
// fresh mount from re-running the DB gap scan when the operator bounces
// between pages. When `instance` is provided the BE scopes the report to
// that one instance; otherwise every instance is returned.
export function useGaps(instance?: string): UseQueryResult<GapReport, ApiError> {
  return useQuery<GapReport, ApiError>({
    queryKey: ['insights', 'gaps', instance ?? 'all'] as const,
    queryFn: () =>
      api<GapReport>(
        '/insights/gaps' + (instance ? `?instance=${encodeURIComponent(instance)}` : ''),
      ),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
