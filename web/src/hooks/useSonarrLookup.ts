// Story 524 / N-4 per-season picker: React Query wrapper around the
// instance sonarr-lookup endpoint. 60s staleTime is generous — the
// modal stays open briefly and the lookup is read-only (no Sonarr-side
// state mutation), so even short-lived cache hits avoid hammering the
// upstream Sonarr metadata provider while operator tweaks the seasons.

import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import {
  getSonarrLookup,
  type SonarrLookupResponse,
} from '@/api/sonarr_lookup';

const SIXTY_SECONDS = 60 * 1000;

export const sonarrLookupKeys = {
  byInstanceTVDB: (instanceName: string, tvdbId: number) =>
    ['sonarr-lookup', instanceName, tvdbId] as const,
};

export function useSonarrLookup(
  instanceName: string | undefined,
  tvdbId: number | undefined,
  enabled = true,
): UseQueryResult<SonarrLookupResponse, ApiError> {
  const name = (instanceName ?? '').trim();
  const id = typeof tvdbId === 'number' && tvdbId > 0 ? tvdbId : 0;
  const eff = enabled && name.length > 0 && id > 0;
  return useQuery<SonarrLookupResponse, ApiError>({
    queryKey: sonarrLookupKeys.byInstanceTVDB(name, id),
    queryFn: () => getSonarrLookup(name, id),
    enabled: eff,
    staleTime: SIXTY_SECONDS,
    retry: false,
  });
}
