import { useQuery, type UseQueryResult } from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Ф6-R-6b Wave B — hand-authored movie-discovery client. The movie discovery
// endpoints are NOT in schema.ts (like the TV /discovery/* surface), so the
// wire types are mirrored by hand from the Go handler:
//   internal/discovery/rest/movie_types.go   (MovieDiscoverItem / …Response)
//   internal/discovery/rest/movie_discover_handler.go (querystring params)
//
// Routes (edge/server.go):
//   GET /api/v1/discovery/movie/discover
//   GET /api/v1/discovery/movie/trending?scope=day|week
//   GET /api/v1/discovery/movie/popular
//   GET /api/v1/discovery/movie/search?q=&lang=&page=

// DiscoveryMovieItem mirrors rest.MovieDiscoverItem struct tags EXACTLY:
//   movie_id int64 (required) · tmdb_id *int (omitempty) · title string ·
//   year *int · poster_hash *string · backdrop_hash *string ·
//   original_language *string · tmdb_rating *float64.
export interface DiscoveryMovieItem {
  readonly movie_id: number;
  readonly tmdb_id?: number;
  readonly title: string;
  readonly year?: number;
  readonly poster_hash?: string;
  readonly backdrop_hash?: string;
  readonly original_language?: string;
  readonly tmdb_rating?: number;
}

// MovieDiscoverCacheStatus mirrors the envelope's cache_status enum.
export type MovieDiscoverCacheStatus = 'hit' | 'miss' | 'warming';

// MovieDiscoverResponse mirrors rest.MovieDiscoverResponse.
export interface MovieDiscoverResponse {
  readonly items: readonly DiscoveryMovieItem[];
  readonly page: number;
  readonly per_page: number;
  readonly cache_status: MovieDiscoverCacheStatus | string;
  readonly degraded?: readonly string[];
  readonly retry_after_seconds?: number;
}

export type MovieTrendingScope = 'day' | 'week';

// Query keys — exported so the add-to-radarr mutation can blow away the movie
// discovery slice after a successful add.
export const movieDiscoveryKeys = {
  all: ['discovery', 'movie'] as const,
  trending: (scope: MovieTrendingScope, lang: string) =>
    ['discovery', 'movie', 'trending', scope, lang] as const,
  popular: (lang: string) => ['discovery', 'movie', 'popular', lang] as const,
  search: (q: string, lang: string) =>
    ['discovery', 'movie', 'search', q, lang] as const,
  discover: (params: Record<string, string>, lang: string) =>
    ['discovery', 'movie', 'discover', params, lang] as const,
};

// withLang appends ?lang= (+ any prior params) — mirrors discovery.ts.
const withLang = (qs: URLSearchParams, lang?: string): string => {
  if (lang) qs.set('lang', lang);
  const s = qs.toString();
  return s ? `?${s}` : '';
};

type MovieListResult = UseQueryResult<MovieDiscoverResponse, ApiError>;

// useMovieTrending — GET /discovery/movie/trending?scope=…
export function useMovieTrending(
  lang?: string,
  scope: MovieTrendingScope = 'day',
): MovieListResult {
  return useQuery<MovieDiscoverResponse, ApiError>({
    queryKey: movieDiscoveryKeys.trending(scope, lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams({ scope });
      return api<MovieDiscoverResponse>(`/discovery/movie/trending${withLang(qs, lang)}`);
    },
    staleTime: 60_000,
  });
}

// useMoviePopular — GET /discovery/movie/popular
export function useMoviePopular(lang?: string): MovieListResult {
  return useQuery<MovieDiscoverResponse, ApiError>({
    queryKey: movieDiscoveryKeys.popular(lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams();
      return api<MovieDiscoverResponse>(`/discovery/movie/popular${withLang(qs, lang)}`);
    },
    staleTime: 60_000,
  });
}

// useMovieSearch — GET /discovery/movie/search?q=… Disabled until q ≥ 2 chars
// so a SearchBar doesn't fire on stray keystrokes (mirror useDiscoverySearch).
export function useMovieSearch(q: string, enabled = true, lang?: string): MovieListResult {
  const trimmed = q.trim();
  const eff = enabled && trimmed.length >= 2;
  return useQuery<MovieDiscoverResponse, ApiError>({
    queryKey: movieDiscoveryKeys.search(trimmed, lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams({ q: trimmed });
      return api<MovieDiscoverResponse>(`/discovery/movie/search${withLang(qs, lang)}`);
    },
    enabled: eff,
    staleTime: 30_000,
  });
}

// useMovieRowDiscover fetches /discovery/movie/discover passing params VERBATIM
// (dotted keys like primary_release_date.gte / with_genres) — the BE
// movie_discover_handler.parse() reads the dotted names directly, so we must
// NOT rename them.
export function useMovieRowDiscover(
  params: Record<string, string>,
  lang: string | undefined,
  enabled: boolean,
): MovieListResult {
  return useQuery<MovieDiscoverResponse, ApiError>({
    queryKey: movieDiscoveryKeys.discover(params, lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams();
      for (const [k, v] of Object.entries(params)) {
        if (v !== '') qs.set(k, v);
      }
      return api<MovieDiscoverResponse>(`/discovery/movie/discover${withLang(qs, lang)}`);
    },
    enabled,
    staleTime: 30_000,
  });
}
