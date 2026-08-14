import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф3.3 — movie ratings wire types. Mirrors movieOverview.ts: keyed on tmdb_id
// (one row per movie), no poll machinery. UNLIKE seriesRatings.ts there is NO
// SWR backoff-poll ladder — movie ratings has no background refresher
// (PRD 2.3) — and the /ratings endpoint takes NO lang param, so neither the
// hook nor its query key thread a language.
export type MovieRatingsResponse = components['schemas']['dto.MovieRatingsResponse'];
export type MovieRatingsSources = components['schemas']['dto.MovieRatingsSources'];

export interface UseMovieRatingsParams {
  readonly tmdbId: number | undefined;
}

// Query key — only the canonical tmdb_id (no lang: /ratings is language-less).
export function movieRatingsQueryKey(
  tmdbId: number,
): readonly ['movie-ratings', number] {
  return ['movie-ratings', tmdbId] as const;
}

// useMovieRatings fetches a movie's aggregated ratings (TMDB + OMDb/IMDb,
// content rating, awards). Enabled only for a positive tmdbId so the
// invalid-param branch never fires a request.
export function useMovieRatings({
  tmdbId,
}: UseMovieRatingsParams): UseQueryResult<MovieRatingsResponse, ApiError> {
  const ready = typeof tmdbId === 'number' && tmdbId > 0;
  return useQuery<MovieRatingsResponse, ApiError>({
    queryKey: ready
      ? movieRatingsQueryKey(tmdbId as number)
      : (['movie-ratings', 0] as const),
    queryFn: () => api<MovieRatingsResponse>(`/movies/${tmdbId}/ratings`),
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
