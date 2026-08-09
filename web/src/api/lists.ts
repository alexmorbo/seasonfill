import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// I-3 wire types — GET /api/v1/insights/lists?instance= → dto.SmartListsReportDTO.
// Typed locally (not from the generated schema): this endpoint ships in the same
// wave as its FE, so the OpenAPI schema regen may lag the page. The shapes mirror
// the BE DTO exactly. Every optional metric field is set only on its owning shelf.

export type SmartListShelfKey = 'ended_incomplete' | 'returning_soon' | 'hiatus';

export interface SmartListSeries {
  series_id: number;
  sonarr_id: number;
  title: string;
  // ended_incomplete → missing_count; returning_soon → next_air_date;
  // hiatus → last_aired_at. Exactly one is set per the owning shelf.
  missing_count?: number;
  next_air_date?: string;
  last_aired_at?: string;
}

export interface SmartListShelf {
  key: SmartListShelfKey;
  // BE machine-stable English fallback label; the FE localizes by `key`.
  title: string;
  // Exact total match count (independent of the capped `series` slice).
  count: number;
  series: SmartListSeries[];
}

export interface SmartListInstance {
  instance_name: string;
  shelves: SmartListShelf[];
}

export interface SmartListReport {
  generated_at: string;
  instances: SmartListInstance[];
}

// useLists fetches the curated "smart lists" report ON DEMAND. Manual-refresh
// page (no refetchInterval); staleTime keeps a page bounce from re-running the
// shelf queries. When `instance` is provided the BE scopes the report to that
// one instance; otherwise every instance is returned.
export function useLists(instance?: string): UseQueryResult<SmartListReport, ApiError> {
  return useQuery<SmartListReport, ApiError>({
    queryKey: ['insights', 'lists', instance ?? 'all'] as const,
    queryFn: () =>
      api<SmartListReport>(
        '/insights/lists' + (instance ? `?instance=${encodeURIComponent(instance)}` : ''),
      ),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
