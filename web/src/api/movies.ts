import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф6-R-6b wire types — the movie vertical. Movies are keyed by tmdb_id (one
// item per movie, deduped across N radarr instances); the poster fields are
// resolved media hashes, rendered via the same /api/v1/media/{hash}
// handler the series posters use (mediaUrl in @/api/series).
export type MovieDetail = components['schemas']['dto.MovieDetailResponse'];
export type MovieDetailLibrary = components['schemas']['dto.MovieDetailLibrary'];
export type MovieDetailCollection = components['schemas']['dto.MovieDetailCollection'];
export type MovieLibraryList = components['schemas']['dto.MovieLibraryList'];
export type MovieLibraryItem = components['schemas']['dto.MovieLibraryItem'];

export type MoviesState = 'all' | 'downloaded' | 'missing';
export type MoviesSort = 'updated_desc' | 'title_asc' | 'release_desc';

export interface MoviesLibraryParams {
  readonly state?: MoviesState;
  readonly sort?: MoviesSort;
  readonly q?: string;
  readonly limit?: number;
  // Cursor is an integer offset (BE emits it as a string in `next_cursor`).
  readonly cursor?: number;
  readonly lang?: string;
}

// Query keys — exported so mutation hooks (Wave B collection add-all /
// monitor) can invalidate the whole movie surface in one shot.
export const movieKeys = {
  all: ['movies'] as const,
  library: (params: MoviesLibraryParams) => ['movies', 'library', params] as const,
  detail: (id: number, lang?: string) => ['movies', 'detail', id, lang ?? ''] as const,
};

function toLibraryQuery(p: MoviesLibraryParams): string {
  const qs = new URLSearchParams();
  if (p.state) qs.set('state', p.state);
  if (p.sort) qs.set('sort', p.sort);
  if (p.q) qs.set('q', p.q);
  if (typeof p.limit === 'number') qs.set('limit', String(p.limit));
  if (typeof p.cursor === 'number') qs.set('cursor', String(p.cursor));
  if (p.lang) qs.set('lang', p.lang);
  const s = qs.toString();
  return s ? `?${s}` : '';
}

// useMovie fetches a single movie detail aggregate. Enabled only for a
// positive tmdbId so the invalid-param branch never fires a request.
export function useMovie(
  tmdbId?: number,
  lang?: string,
): UseQueryResult<MovieDetail, ApiError> {
  const enabled = typeof tmdbId === 'number' && tmdbId > 0;
  return useQuery<MovieDetail, ApiError>({
    queryKey: enabled
      ? movieKeys.detail(tmdbId as number, lang)
      : movieKeys.detail(0, lang),
    queryFn: () =>
      api<MovieDetail>(
        `/movies/${tmdbId}` + (lang ? `?lang=${encodeURIComponent(lang)}` : ''),
      ),
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}

// useMoviesLibrary fetches one page of the movie library list. Pagination is
// cursor-based (int offset): pass the numeric form of `next_cursor` from the
// previous envelope to fetch the next page. Passes `?lang=` (movie_i18n batch
// title ladder, M-FIX-2).
export function useMoviesLibrary(
  params: MoviesLibraryParams,
): UseQueryResult<MovieLibraryList, ApiError> {
  return useQuery<MovieLibraryList, ApiError>({
    queryKey: movieKeys.library(params),
    queryFn: () => api<MovieLibraryList>(`/movies${toLibraryQuery(params)}`),
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
