import {
  useMutation, useQueryClient, type UseMutationResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import { movieDiscoveryKeys } from '@/api/discoveryMovies';
import { movieKeys } from '@/api/movies';
import { movieCollectionKeys } from '@/api/movieCollections';

// Ф6-R-6b Wave B — POST /api/v1/discovery/add-to-radarr. Wire shape mirrors
// the Go decode/encode structs in
//   internal/discovery/rest/add_to_radarr_handler.go
//     addToRadarrRequest  { instance_name, tmdb_id, quality_profile_id,
//                           root_folder_path, monitored?, minimum_availability?,
//                           search_on_add? }
//     addToRadarrResponse { radarr_movie_id, instance_name, already_added }
//
// The handler decodes with DisallowUnknownFields, so the request MUST carry
// only these keys — omit optionals via conditional spread rather than sending
// undefined (which JSON.stringify drops anyway, but we keep the type exact).

// MinimumAvailability enum — the exact strings Radarr's /api/v3/movie accepts.
// Confirmed against internal/shared/clients/radarr (announced|inCinemas|
// released; client defaults "" → "released", ADR-0018 Q3). 'tba' is a valid
// Radarr value too but is not surfaced in this UI.
export type MinimumAvailability = 'announced' | 'inCinemas' | 'released';

export interface AddToRadarrRequest {
  readonly instance_name: string;
  readonly tmdb_id: number;
  readonly quality_profile_id: number;
  readonly root_folder_path: string;
  readonly monitored?: boolean;
  readonly minimum_availability?: MinimumAvailability;
  readonly search_on_add?: boolean;
}

export interface AddToRadarrResponse {
  readonly radarr_movie_id: number;
  readonly instance_name: string;
  readonly already_added: boolean;
}

// useAddToRadarr posts the add. On success it invalidates the movie discovery
// slice, the whole movie surface (library membership chips), and the movie
// collection cache so every dependent view refreshes on next render.
export function useAddToRadarr(): UseMutationResult<
  AddToRadarrResponse, ApiError, AddToRadarrRequest
> {
  const qc = useQueryClient();
  return useMutation<AddToRadarrResponse, ApiError, AddToRadarrRequest>({
    mutationFn: (body) => api<AddToRadarrResponse>('/discovery/add-to-radarr', {
      method: 'POST', body,
    }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: movieDiscoveryKeys.all });
      void qc.invalidateQueries({ queryKey: movieKeys.all });
      void qc.invalidateQueries({ queryKey: movieCollectionKeys.all });
    },
  });
}
