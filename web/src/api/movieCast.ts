import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф3.1 — movie cast wire types. Mirrors seriesCast.ts but keyed on tmdb_id
// (one row per movie) and consuming the movie DTOs. BE returns cast in
// credit_order ASC NULLS LAST; the FE renders that order verbatim (no
// re-sort). `served_language` + `degraded` (["missing_lang"]) drive the
// localized-title fallback signal in MovieCastStrip.
export type MovieCastResponse = components['schemas']['dto.MovieCastResponse'];
export type MovieCastMember = components['schemas']['dto.MovieCastMember'];

// BE sort vocabulary for /movies/{tmdb_id}/cast: `credit` (default) | `name`.
export type MovieCastSort = 'credit' | 'name';

export interface UseMovieCastParams {
  readonly tmdbId: number | undefined;
  readonly lang?: string | undefined;
  // Omitted/undefined → `credit` (BE default), so the URL stays clean.
  readonly sort?: MovieCastSort | undefined;
}

// Query key — lang + sort folded in so the cache never collides across
// languages or sort orders (same discipline as seriesCastQueryKey).
export function movieCastQueryKey(
  tmdbId: number,
  lang: string,
  sort: MovieCastSort = 'credit',
): readonly [string, number, string, MovieCastSort] {
  return ['movie-cast', tmdbId, lang, sort] as const;
}

// useMovieCast fetches a movie's full cast. Enabled only for a positive
// tmdbId so the invalid-param branch never fires a request.
export function useMovieCast({
  tmdbId,
  lang,
  sort,
}: UseMovieCastParams): UseQueryResult<MovieCastResponse, ApiError> {
  const ready = typeof tmdbId === 'number' && tmdbId > 0;
  const effectiveLang = lang ?? '';
  const effectiveSort: MovieCastSort = sort ?? 'credit';
  return useQuery<MovieCastResponse, ApiError>({
    queryKey: ready
      ? movieCastQueryKey(tmdbId as number, effectiveLang, effectiveSort)
      : (['movie-cast', 0, '', 'credit'] as const),
    queryFn: () => {
      const params = new URLSearchParams();
      if (effectiveLang) params.set('lang', effectiveLang);
      if (effectiveSort !== 'credit') params.set('sort', effectiveSort);
      const qs = params.toString() ? `?${params.toString()}` : '';
      return api<MovieCastResponse>(`/movies/${tmdbId}/cast${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
