import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// I-5 wire types — GET /api/v1/insights/collections?instance= →
// dto.CollectionsReportDTO. Typed locally (not from the generated schema): this
// endpoint ships in the same wave as its FE, so the OpenAPI schema regen may lag
// the page. The shapes mirror the BE DTO exactly.

export interface CollectionSeries {
  series_id: number;
  sonarr_id: number;
  title: string;
}

export interface Collection {
  slug: string;
  // BE machine-stable English fallback label; the FE localizes by `slug`.
  title: string;
  // Exact owned total (independent of the capped `series` slice).
  owned_count: number;
  is_franchise: boolean;
  series: CollectionSeries[];
}

export interface CollectionsInstance {
  instance_name: string;
  collections: Collection[];
}

export interface CollectionsReport {
  generated_at: string;
  instances: CollectionsInstance[];
}

// useCollections fetches the curated collections report ON DEMAND. Manual-refresh
// page (no refetchInterval); staleTime keeps a page bounce from re-running the
// queries. When `instance` is provided the BE scopes the report to that one
// instance; otherwise every instance is returned.
export function useCollections(
  instance?: string,
): UseQueryResult<CollectionsReport, ApiError> {
  return useQuery<CollectionsReport, ApiError>({
    queryKey: ['insights', 'collections', instance ?? 'all'] as const,
    queryFn: () =>
      api<CollectionsReport>(
        '/insights/collections' +
          (instance ? `?instance=${encodeURIComponent(instance)}` : ''),
      ),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
