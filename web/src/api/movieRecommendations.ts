import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф3.4 — movie recommendations wire types. Mirrors movieRatings.ts
// (tmdb_id-keyed, ApiError-typed) + seriesRecommendations.ts (?lang= thread).
// UNLIKE the series recs hook there is NO degraded-poll ladder here: the movie
// recs endpoint has no background refresher and the rail does not re-poll.
export type MovieRecommendationsResponse =
  components['schemas']['dto.MovieRecommendationsResponse'];
export type MovieRecommendation =
  components['schemas']['dto.MovieRecommendation'];

export interface UseMovieRecommendationsParams {
  readonly tmdbId: number | undefined;
  readonly limit?: number | undefined;
  readonly offset?: number | undefined;
  // BCP-47 tag forwarded as `?lang=` so the BE emits localised rec titles.
  // Threaded into the queryKey so TanStack isolates cache per language.
  readonly lang?: string | undefined;
  // Caller gate (e.g. section intersection). When false the query is disabled.
  readonly enabled?: boolean | undefined;
}

const DEFAULT_LIMIT = 20;
const DEFAULT_OFFSET = 0;

export function movieRecommendationsQueryKey(
  tmdbId: number,
  limit: number,
  offset: number,
  lang: string,
): readonly ['movie-recommendations', number, number, number, string] {
  return ['movie-recommendations', tmdbId, limit, offset, lang] as const;
}

// useMovieRecommendations fetches a movie's rank-ordered recommendations.
// Enabled only for a positive tmdbId (and when `enabled` is not false) so the
// invalid-param branch never fires a request.
export function useMovieRecommendations({
  tmdbId,
  limit,
  offset,
  lang,
  enabled,
}: UseMovieRecommendationsParams): UseQueryResult<MovieRecommendationsResponse, ApiError> {
  const effectiveLimit = limit ?? DEFAULT_LIMIT;
  const effectiveOffset = offset ?? DEFAULT_OFFSET;
  const effectiveLang = lang ?? '';
  const ready = (enabled ?? true) && typeof tmdbId === 'number' && tmdbId > 0;
  return useQuery<MovieRecommendationsResponse, ApiError>({
    queryKey: ready
      ? movieRecommendationsQueryKey(tmdbId as number, effectiveLimit, effectiveOffset, effectiveLang)
      : (['movie-recommendations', 0, effectiveLimit, effectiveOffset, effectiveLang] as const),
    queryFn: () => {
      const langQs = effectiveLang ? `&lang=${encodeURIComponent(effectiveLang)}` : '';
      return api<MovieRecommendationsResponse>(
        `/movies/${tmdbId}/recommendations?limit=${effectiveLimit}&offset=${effectiveOffset}${langQs}`,
      );
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
