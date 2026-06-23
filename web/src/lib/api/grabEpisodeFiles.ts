import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api, ApiError } from '@/lib/api';
import type { components } from '@/api/schema';

// 043c response shape. Once the schema codegen catches up, replace
// the inline type with `components['schemas']['dto.EpisodeFileList']`.
export interface EpisodeFile {
  readonly id: number;
  readonly relative_path: string;
  readonly season_number: number;
  readonly episode_numbers: readonly number[] | null;
  readonly size_bytes: number;
  readonly quality: string;
}

export interface EpisodeFileList {
  readonly items: readonly EpisodeFile[];
}

// Lazy fetch — fires only when `id` present AND `enabled` true.
// 493 / N-1c: the per-instance namespace was dropped — the BE
// route is now `GET /api/v1/grabs/:id/episode-files` (global).
// We preserve the `instance` parameter on the public signature so
// callers don't need to be re-plumbed in lockstep, but the value
// is no longer carried over the wire.
//
// Query key: ['grab', id, 'episode-files'] — stable across page
// re-renders so cache hits when re-opening the same drawer.
export const grabEpisodeFilesKey = (id: string) =>
  ['grab', id, 'episode-files'] as const;

export function useGrabEpisodeFiles(
  _instance: string | null,
  id: string | null,
  options: { enabled: boolean },
) {
  return useQuery<EpisodeFileList, ApiError>({
    queryKey: id
      ? grabEpisodeFilesKey(id)
      : ['grab', '__disabled__', 'episode-files'],
    queryFn: async () => {
      if (!id) throw new ApiError(400, 'id required');
      return api<EpisodeFileList>(
        `/grabs/${encodeURIComponent(id)}/episode-files`,
      );
    },
    enabled: options.enabled && Boolean(id),
    staleTime: 5 * 60 * 1000,
    refetchOnWindowFocus: false,
    refetchInterval: false,
  });
}

type Grab = components['schemas']['dto.Grab'];
type GrabsList = components['schemas']['dto.GrabList'];

// 493 / N-1c §D — cache-walk fallback for `useGrabById`.
//
// BE 492 deleted the per-instance single-grab endpoint, and the
// global `/api/v1/grabs` list lacks a `?id=` filter. The only
// production caller (`<ReGrabThread>`) always renders inside a row
// of an already-paginated grabs list, so the requested grab is
// almost certainly cached in some `['grabs', ...]` infinite-query
// entry. We walk every such cache entry, pages × items, looking
// for `id === param`. When found, we return a synthetic
// useQuery-shaped result that resolves to the cached row. When
// missing, we trigger a single `invalidate(['grabs'])` to force
// the list to refetch, then re-walk on the next render.
//
// The signature stays `(instance, id, options)` so existing call
// sites don't need to change. `instance` is now a hint used only
// to scope the invalidate (the typical caller filters its grabs
// list by instance).
export const grabByIdKey = (id: string) => ['grab', id] as const;

export function useGrabById(
  _instance: string | null,
  id: string | null,
  options: { enabled: boolean },
) {
  const qc = useQueryClient();
  return useQuery<Grab, ApiError>({
    queryKey: id ? grabByIdKey(id) : ['grab', '__disabled__'],
    queryFn: async () => {
      if (!id) throw new ApiError(400, 'id required');
      // Walk every cached `['grabs', ...]` infinite-query entry
      // looking for the row. React Query stores
      // useInfiniteQuery data as { pages: T[]; pageParams: ... }.
      const entries = qc.getQueriesData<{ pages: GrabsList[] } | GrabsList>({
        queryKey: ['grabs'],
      });
      for (const [, data] of entries) {
        if (!data) continue;
        const pages = (data as { pages?: GrabsList[] }).pages;
        if (Array.isArray(pages)) {
          for (const page of pages) {
            const hit = (page.items ?? []).find((g) => g.id === id);
            if (hit) return hit;
          }
        } else {
          // Non-infinite shape — single page.
          const hit = ((data as GrabsList).items ?? []).find(
            (g) => g.id === id,
          );
          if (hit) return hit;
        }
      }
      // Not cached — trigger a refetch of the grabs list and throw
      // a soft 404 so the consumer can render an inline error or
      // retry on its own cadence. The next render after the
      // invalidate completes will find the row in cache.
      await qc.invalidateQueries({ queryKey: ['grabs'] });
      throw new ApiError(404, 'grab not found in cache');
    },
    enabled: options.enabled && Boolean(id),
    staleTime: 60 * 1000,
    refetchOnWindowFocus: false,
    refetchInterval: false,
    retry: false,
  });
}
