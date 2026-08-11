import {
  useMutation, useQuery, useQueryClient,
  type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';
import { movieKeys } from '@/api/movies';

// Ф6-R-6b Wave B wire types — the movie-collection endpoints. Created now so
// Wave B (collection block + add-all-missing split-button on MovieDetail)
// only has to import. DISTINCT from @/api/collections.ts, which is the Ф7
// insight report — do NOT confuse the two.
export type MovieCollectionDetail = components['schemas']['dto.MovieCollectionDetail'];
export type MovieCollectionPartDTO = components['schemas']['dto.MovieCollectionPartDTO'];
export type MovieCollectionAddAllRequest =
  components['schemas']['dto.MovieCollectionAddAllRequest'];
export type MovieCollectionAddAllResponse =
  components['schemas']['dto.MovieCollectionAddAllResponse'];
export type MovieCollectionAddPartDTO =
  components['schemas']['dto.MovieCollectionAddPartDTO'];
export type MovieCollectionMonitorRequest =
  components['schemas']['dto.MovieCollectionMonitorRequest'];

export const movieCollectionKeys = {
  all: ['movie-collections'] as const,
  detail: (id: number, instance?: string) =>
    ['movie-collections', 'detail', id, instance ?? ''] as const,
};

// useMovieCollection — GET /collections/:id[?instance=]. Enabled only for a
// positive collection id.
export function useMovieCollection(
  id?: number,
  instance?: string,
): UseQueryResult<MovieCollectionDetail, ApiError> {
  const enabled = typeof id === 'number' && id > 0;
  return useQuery<MovieCollectionDetail, ApiError>({
    queryKey: enabled
      ? movieCollectionKeys.detail(id as number, instance)
      : movieCollectionKeys.detail(0, instance),
    queryFn: () =>
      api<MovieCollectionDetail>(
        `/collections/${id}` +
          (instance ? `?instance=${encodeURIComponent(instance)}` : ''),
      ),
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

export interface AddAllMissingVars {
  readonly collectionId: number;
  readonly body: MovieCollectionAddAllRequest;
}

// useAddAllMissing — POST /collections/:id/add-all-missing. On success blows
// away the collection cache + the whole movie surface so membership chips
// refresh on the next render.
export function useAddAllMissing(): UseMutationResult<
  MovieCollectionAddAllResponse, ApiError, AddAllMissingVars
> {
  const qc = useQueryClient();
  return useMutation<MovieCollectionAddAllResponse, ApiError, AddAllMissingVars>({
    mutationFn: ({ collectionId, body }) =>
      api<MovieCollectionAddAllResponse>(
        `/collections/${collectionId}/add-all-missing`,
        { method: 'POST', body },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: movieCollectionKeys.all });
      void qc.invalidateQueries({ queryKey: movieKeys.all });
    },
  });
}

export interface SetCollectionMonitorVars {
  readonly collectionId: number;
  readonly body: MovieCollectionMonitorRequest;
}

// useSetCollectionMonitor — PUT /collections/:id/monitor.
export function useSetCollectionMonitor(): UseMutationResult<
  void, ApiError, SetCollectionMonitorVars
> {
  const qc = useQueryClient();
  return useMutation<void, ApiError, SetCollectionMonitorVars>({
    mutationFn: ({ collectionId, body }) =>
      api<void>(`/collections/${collectionId}/monitor`, {
        method: 'PUT', body,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: movieCollectionKeys.all });
      void qc.invalidateQueries({ queryKey: movieKeys.all });
    },
  });
}
