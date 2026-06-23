import {
  useInfiniteQuery,
  type UseInfiniteQueryResult,
} from '@tanstack/react-query';
import { api, ApiError } from '@/lib/api';
import type { components } from '@/api/schema';

// dto.GrabList is the wire shape returned by GET /api/v1/grabs.
// Keyset-paginated — single `next_cursor` field, no `has_more`
// boolean. Pagination terminates when the response omits
// `next_cursor`.
export type GrabsListResponse = components['schemas']['dto.GrabList'];
export type Grab = components['schemas']['dto.Grab'];

export interface UseGrabsParams {
  // Optional — omit to fetch across all instances (audit aggregate).
  readonly instance?: string | undefined;
  // 'pending' | 'imported' | 'failed' | ... — wire enum dto.GrabStatus.
  readonly status?: string | undefined;
  readonly limit?: number;
}

export const grabsKey = (
  p: UseGrabsParams,
): readonly ['grabs', UseGrabsParams] => ['grabs', p] as const;

function buildPath(p: UseGrabsParams, cursor: string): string {
  const params = new URLSearchParams();
  if (p.instance) params.set('instance', p.instance);
  if (p.status) params.set('status', p.status);
  if (p.limit !== undefined) params.set('limit', String(p.limit));
  if (cursor) params.set('cursor', cursor);
  const qs = params.toString();
  return `/grabs${qs ? `?${qs}` : ''}`;
}

export function useGrabs(
  p: UseGrabsParams,
  opts: { enabled?: boolean; refetchInterval?: number } = {},
): UseInfiniteQueryResult<
  { pages: GrabsListResponse[]; pageParams: string[] },
  ApiError
> {
  return useInfiniteQuery<
    GrabsListResponse,
    ApiError,
    { pages: GrabsListResponse[]; pageParams: string[] },
    ReturnType<typeof grabsKey>,
    string
  >({
    queryKey: grabsKey(p),
    queryFn: ({ pageParam }) =>
      api<GrabsListResponse>(buildPath(p, pageParam)),
    initialPageParam: '',
    getNextPageParam: (last) =>
      last.next_cursor ? last.next_cursor : undefined,
    enabled: opts.enabled ?? true,
    staleTime: 30_000,
    refetchInterval: opts.refetchInterval ?? false,
    refetchOnWindowFocus: false,
  });
}

export function flattenGrabPages(
  pages: readonly GrabsListResponse[] | undefined,
): readonly Grab[] {
  return pages ? pages.flatMap((p) => p.items ?? []) : [];
}
