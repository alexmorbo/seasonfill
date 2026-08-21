import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { components } from '@/api/schema';

// movieTorrents.ts — the movie twin of seriesTorrents.ts (B1.5, ADR-0023).
//
// Deliberately NOT merged into seriesTorrents.ts: the frozen SeriesDetail
// test suite mocks '@/api/seriesTorrents' wholesale (vi.importActual +
// override useIsSectionVisible) and depends on that module's export
// surface / query-key shape staying untouched. Keeping the movie fetch in
// its own file means seriesTorrents.ts needs zero edits, so that mock stays
// valid byte-for-byte.
//
// `useIsSectionVisible` itself IS media-agnostic (pure tab-visibility +
// IntersectionObserver composer, no series coupling) so it is imported
// from seriesTorrents.ts by MovieTorrentsSection.tsx rather than
// duplicated here.
export type MovieTorrentsResponse = components['schemas']['dto.MovieTorrentsResponse'];

// TorrentRow is intentionally NOT redeclared here — it's the same
// generated DTO (components['schemas']['dto.TorrentRow']) backing both
// endpoints; `provenance` is simply omitempty on series rows (see
// schema.ts's dto.TorrentRow.provenance docstring). Import it from
// seriesTorrents.ts (single source of truth) — every existing torrents/*
// leaf component already does this.

export interface UseMovieTorrentsParams {
  readonly tmdbId: number | undefined;
  // visible drives refetchInterval gating — same composer contract as
  // useSeriesTorrents (see seriesTorrents.ts's doc comment for the
  // gating-layer rationale).
  readonly visible: boolean;
  // enabled lets the page-level qBit-configured check disable the query
  // entirely (no key, no fetch). Default `true` for tests.
  readonly enabled?: boolean | undefined;
}

export function movieTorrentsQueryKey(
  tmdbId: number,
): readonly ['movie-torrents', number] {
  return ['movie-torrents', tmdbId] as const;
}

// useMovieTorrents — pollable per-movie torrents inventory, keyed by TMDB
// id (the movie API surface is TMDB-keyed throughout — see MovieDetail.tsx
// / GET /movies/{tmdb_id}/torrents). Gating layers and poll cadence are
// byte-identical to useSeriesTorrents; see that function's doc comment.
export function useMovieTorrents({
  tmdbId,
  visible,
  enabled = true,
}: UseMovieTorrentsParams): UseQueryResult<MovieTorrentsResponse> {
  const ready =
    enabled && typeof tmdbId === 'number' && tmdbId > 0;
  return useQuery<MovieTorrentsResponse>({
    queryKey: ready
      ? movieTorrentsQueryKey(tmdbId as number)
      : (['movie-torrents', 0] as const),
    queryFn: () =>
      api<MovieTorrentsResponse>(
        `/movies/${tmdbId}/torrents`,
      ),
    enabled: ready,
    refetchInterval: visible ? 3000 : false,
    refetchOnWindowFocus: true,
    staleTime: 0,
  });
}
