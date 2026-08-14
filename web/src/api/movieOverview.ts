import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';
import type { components } from '@/api/schema';

// Ф3.2 — movie overview wire type. Mirrors movieCast.ts: keyed on tmdb_id
// (one row per movie), no poll machinery. `served_language` + `degraded`
// (["missing_lang"]) drive the localized-title fallback signal in
// MovieOverviewBlock via LanguageFallbackTag.
export type MovieOverviewResponse = components['schemas']['dto.MovieOverviewResponse'];

export interface UseMovieOverviewParams {
  readonly tmdbId: number | undefined;
  readonly lang?: string | undefined;
}

// Query key — lang folded in so the cache never collides across languages
// (same discipline as movieCastQueryKey / seriesOverviewQueryKey).
export function movieOverviewQueryKey(
  tmdbId: number,
  lang: string,
): readonly [string, number, string] {
  return ['movie-overview', tmdbId, lang] as const;
}

// useMovieOverview fetches a movie's localized overview block. Enabled only
// for a positive tmdbId so the invalid-param branch never fires a request.
export function useMovieOverview({
  tmdbId,
  lang,
}: UseMovieOverviewParams): UseQueryResult<MovieOverviewResponse, ApiError> {
  const ready = typeof tmdbId === 'number' && tmdbId > 0;
  const effectiveLang = lang ?? '';
  return useQuery<MovieOverviewResponse, ApiError>({
    queryKey: ready
      ? movieOverviewQueryKey(tmdbId as number, effectiveLang)
      : (['movie-overview', 0, ''] as const),
    queryFn: () => {
      const params = new URLSearchParams();
      if (effectiveLang) params.set('lang', effectiveLang);
      const qs = params.toString() ? `?${params.toString()}` : '';
      return api<MovieOverviewResponse>(`/movies/${tmdbId}/overview${qs}`);
    },
    enabled: ready,
    staleTime: 30_000,
    refetchOnWindowFocus: false,
  });
}
