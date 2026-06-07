import { useQuery } from '@tanstack/react-query';
import { api, ApiError } from '@/lib/api';
import type { components } from '@/api/schema';

// 043c response shape. Once the schema codegen catches up, replace
// the inline type with `components['schemas']['dto.EpisodeFileList']`.
export interface EpisodeFile {
  readonly id: number;
  readonly relative_path: string;
  readonly season_number: number;
  readonly episode_numbers: readonly number[];
  readonly size_bytes: number;
  readonly quality: string;
}

export interface EpisodeFileList {
  readonly items: readonly EpisodeFile[];
}

// Lazy fetch — fires only when both inputs present AND `enabled` true.
//
// Query key: ['grab', instance, id, 'episode-files'] — stable across
// page re-renders so cache hits when re-opening the same drawer in
// a session.
export const grabEpisodeFilesKey = (instance: string, id: string) =>
  ['grab', instance, id, 'episode-files'] as const;

export function useGrabEpisodeFiles(
  instance: string | null,
  id: string | null,
  options: { enabled: boolean },
) {
  return useQuery<EpisodeFileList, ApiError>({
    queryKey: instance && id
      ? grabEpisodeFilesKey(instance, id)
      : ['grab', '__disabled__', 'episode-files'],
    queryFn: async () => {
      if (!instance || !id) throw new ApiError(400, 'instance and id required');
      return api<EpisodeFileList>(
        `/instances/${encodeURIComponent(instance)}/grabs/${encodeURIComponent(id)}/episode-files`,
      );
    },
    enabled: options.enabled && Boolean(instance) && Boolean(id),
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: false,
    refetchInterval: false,
  });
}

// Used by ReGrabThread when an ancestor is not in the locally cached
// paginated list. Single grab fetch.
export const grabByIdKey = (instance: string, id: string) =>
  ['grab', instance, id] as const;

export function useGrabById(
  instance: string | null,
  id: string | null,
  options: { enabled: boolean },
) {
  return useQuery<components['schemas']['dto.Grab'], ApiError>({
    queryKey: instance && id ? grabByIdKey(instance, id) : ['grab', '__disabled__'],
    queryFn: async () => {
      if (!instance || !id) throw new ApiError(400, 'instance and id required');
      return api<components['schemas']['dto.Grab']>(
        `/instances/${encodeURIComponent(instance)}/grabs/${encodeURIComponent(id)}`,
      );
    },
    enabled: options.enabled && Boolean(instance) && Boolean(id),
    staleTime: 60 * 1000,
    refetchOnWindowFocus: false,
    refetchInterval: false,
  });
}
