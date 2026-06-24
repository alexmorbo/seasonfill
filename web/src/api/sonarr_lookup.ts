// Story 524 / N-4 per-season picker: hand-authored DTO for the lookup
// endpoint. Mirrors internal/admin/rest/instance_metadata_handler.go
// (sonarrLookupResponse + sonarrLookupSeasonDTO).
//
// The BE returns the seasons preview straight from Sonarr's lookup
// provider (no persistence). The AddToSonarrModal uses it to populate
// per-season checkboxes — default selection mirrors Sonarr's own
// `monitored` flag (which today means: every season except specials).

import { api } from '@/lib/api';

export interface SonarrLookupSeason {
  readonly season_number: number;
  readonly episode_count: number;
  readonly monitored: boolean;
}

export interface SonarrLookupResponse {
  readonly items: readonly SonarrLookupSeason[];
  readonly title: string;
  readonly year: number;
  readonly overview: string;
  readonly image_url: string;
  readonly tvdb_id: number;
  readonly tmdb_id: number;
  readonly instance_name: string;
}

export function getSonarrLookup(
  instanceName: string,
  tvdbId: number,
): Promise<SonarrLookupResponse> {
  const qs = new URLSearchParams({ tvdb_id: String(tvdbId) });
  return api<SonarrLookupResponse>(
    `/instances/${encodeURIComponent(instanceName)}/sonarr-lookup?${qs.toString()}`,
  );
}
