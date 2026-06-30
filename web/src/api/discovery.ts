import {
  useMutation, useQuery, useQueryClient,
  type Query, type UseMutationResult, type UseQueryResult,
} from '@tanstack/react-query';
import { ApiError, api } from '@/lib/api';

// Story 513 / N-3a: hand-authored DTOs (handlers not yet in schema.ts).
// Mirrors internal/shared/http/edge/server.go:285-301 contracts.

export interface DiscoverySeriesItem {
  readonly series_id: number;
  readonly tmdb_id: number;
  // tvdb_id is optional today: the discovery endpoints don't populate
  // it for every TMDB-only stub. Story 522 / N-4e Add-to-Sonarr requires
  // it (Sonarr identifies series by TVDB id during /api/v3/series add),
  // so the modal disables submit when this field is missing and the
  // future BE projection enrichment lands separately.
  readonly tvdb_id?: number;
  readonly title: string;
  readonly year?: number;
  // Story 554 / E-1 Z5: hash-addressed content URL for /api/v1/media/:hash.
  // PREFER this over the legacy poster_path / backdrop_path pair below —
  // the legacy fields will be dropped in a Z5-cleanup follow-up once the
  // FE bundle CDN cache window expires (~7d post-deploy).
  readonly poster_hash?: string;
  readonly backdrop_hash?: string;
  readonly poster_path?: string;   // legacy — mirror of poster_hash
  readonly backdrop_path?: string; // legacy — mirror of backdrop_hash
  readonly origin_countries?: readonly string[];
  readonly genres?: readonly string[];
  readonly in_library_instances?: readonly string[];
}

export type CacheStatus = 'hit' | 'miss' | 'warming' | 'stale';

// Story 517 / N-3e consumes degraded/warming_*/retry_*; declared now for stable types.
export interface DiscoveryListResponse {
  readonly items: readonly DiscoverySeriesItem[];
  readonly refreshed_at?: string;
  readonly cache_status?: CacheStatus;
  readonly degraded?: readonly string[];
  readonly warming_estimate_seconds?: number;
  readonly retry_after_seconds?: number;
}

export interface DiscoveryGenre { readonly id: number; readonly name: string }
export interface DiscoveryNetwork {
  readonly id: number; readonly name: string; readonly logo_path?: string;
}
export interface DiscoveryGenresResponse { readonly items: readonly DiscoveryGenre[] }
export interface DiscoveryNetworksResponse { readonly items: readonly DiscoveryNetwork[] }

export interface DiscoveryFilter {
  readonly with_genres?: readonly number[];
  readonly first_air_date_gte?: string;
  readonly first_air_date_lte?: string;
  readonly with_origin_country?: readonly string[];
  readonly with_networks?: readonly number[];
  readonly with_status?: readonly string[];
  readonly with_type?: readonly string[];
  readonly sort_by?: string;
  readonly vote_average_gte?: number;
  readonly vote_average_lte?: number;
  readonly page?: number;
}

// Query keys — exported so 514-517 invalidate slices without string-guessing.
// `all` is the umbrella prefix used by 522 to invalidate every discovery
// slice in one shot after a successful POST /discovery/add-to-sonarr.
export const discoveryKeys = {
  all: ['discovery'] as const,
  trending: (lang: string) => ['discovery', 'trending', lang] as const,
  popular: (lang: string) => ['discovery', 'popular', lang] as const,
  byGenre: (id: number, lang: string) => ['discovery', 'genre', id, lang] as const,
  byNetwork: (id: number, lang: string) => ['discovery', 'network', id, lang] as const,
  byKeyword: (id: number, lang: string) => ['discovery', 'keyword', id, lang] as const,
  genresList: (lang: string) => ['discovery', 'genres-list', lang] as const,
  networksList: (lang: string) => ['discovery', 'networks-list', lang] as const,
  search: (q: string, lang: string) => ['discovery', 'search', q, lang] as const,
  discover: (f: DiscoveryFilter, lang: string) => ['discovery', 'discover', f, lang] as const,
};

// Story 522 / N-4e: POST /api/v1/discovery/add-to-sonarr request +
// response bodies. Wire shape mirrors
// internal/discovery/rest/add_to_sonarr_handler.go — `tvdb_id` is the
// required identifier (sonarr matches by TVDB id during add), the
// instance_name picks which Sonarr to write to, and the response
// surfaces the resolved `user_tag_label` ("sf-{username}" or "sf-system")
// + the Sonarr-side series id.
export type AddToSonarrMonitorMode = 'all' | 'future' | 'missing' | 'none';

export interface AddToSonarrRequest {
  readonly instance_name: string;
  readonly tvdb_id: number;
  readonly quality_profile_id: number;
  readonly root_folder_path: string;
  readonly monitor_mode?: AddToSonarrMonitorMode;
  readonly monitored?: boolean;
  readonly search_on_add?: boolean;
  // Story 524 / N-4 per-season picker: when omitted/empty the BE keeps
  // the legacy monitor_mode semantics; when present, the BE writes
  // per-season explicit monitored flags.
  readonly monitored_seasons?: readonly number[];
}

export interface AddToSonarrResponse {
  readonly sonarr_series_id: number;
  readonly instance_name: string;
  readonly user_tag_label: string;
  readonly user_tag_id: number;
}

// useAddToSonarr is the mutation hook the modal calls. On success it
// blasts away the entire `discovery` cache so every card's
// `in_library_instances` count refreshes on the next render. We let
// React Query refetch lazily — the modal closes immediately after
// success.
export function useAddToSonarr(): UseMutationResult<
  AddToSonarrResponse, ApiError, AddToSonarrRequest
> {
  const qc = useQueryClient();
  return useMutation<AddToSonarrResponse, ApiError, AddToSonarrRequest>({
    mutationFn: (body) => api<AddToSonarrResponse>('/discovery/add-to-sonarr', {
      method: 'POST', body,
    }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: discoveryKeys.all });
    },
  });
}

const langQs = (lang?: string) => (lang ? `?lang=${encodeURIComponent(lang)}` : '');
const withLang = (qs: URLSearchParams, lang?: string) => {
  if (lang) qs.set('lang', lang);
  const s = qs.toString();
  return s ? `?${s}` : '';
};

type ListResult = UseQueryResult<DiscoveryListResponse, ApiError>;

// Story 517 / N-3e: `refetchInterval` lets call sites drive polling while
// the backend reports a degraded slice. Callback form receives the latest
// query so the interval can be derived from `degraded`/`retry_after_seconds`
// on the same observer (no double-subscription). `false` (default) keeps
// prior behaviour — single fetch + manual invalidation.
export type DiscoveryQuery = Query<DiscoveryListResponse, ApiError>;
export type RefetchInterval =
  | number
  | false
  | ((q: DiscoveryQuery) => number | false | undefined);

// Trending + Popular share the same shape; factor into one helper.
function useDiscoveryList(
  kind: 'trending' | 'popular',
  lang: string | undefined,
  refetchInterval: RefetchInterval,
): ListResult {
  const key = kind === 'trending'
    ? discoveryKeys.trending(lang ?? '') : discoveryKeys.popular(lang ?? '');
  return useQuery<DiscoveryListResponse, ApiError>({
    queryKey: key,
    queryFn: () => api<DiscoveryListResponse>(`/discovery/${kind}${langQs(lang)}`),
    staleTime: 60_000,
    refetchInterval,
  });
}
export const useDiscoveryTrending = (
  lang?: string, refetchInterval: RefetchInterval = false,
) => useDiscoveryList('trending', lang, refetchInterval);
export const useDiscoveryPopular = (
  lang?: string, refetchInterval: RefetchInterval = false,
) => useDiscoveryList('popular', lang, refetchInterval);

export function useDiscoveryByGenre(
  genreId: number | undefined,
  lang?: string,
  refetchInterval: RefetchInterval = false,
): ListResult {
  const enabled = typeof genreId === 'number' && genreId > 0;
  return useQuery<DiscoveryListResponse, ApiError>({
    queryKey: enabled
      ? discoveryKeys.byGenre(genreId as number, lang ?? '')
      : (['discovery', 'genre', 0, ''] as const),
    queryFn: () =>
      api<DiscoveryListResponse>(`/discovery/genre/${genreId}${langQs(lang)}`),
    enabled,
    staleTime: 60_000,
    refetchInterval,
  });
}

export function useDiscoveryGenresList(
  lang?: string,
): UseQueryResult<DiscoveryGenresResponse, ApiError> {
  return useQuery<DiscoveryGenresResponse, ApiError>({
    queryKey: discoveryKeys.genresList(lang ?? ''),
    queryFn: () => api<DiscoveryGenresResponse>(`/discovery/genres${langQs(lang)}`),
    staleTime: 24 * 60 * 60_000,
  });
}

export function useDiscoveryNetworksList(
  lang?: string,
): UseQueryResult<DiscoveryNetworksResponse, ApiError> {
  return useQuery<DiscoveryNetworksResponse, ApiError>({
    queryKey: discoveryKeys.networksList(lang ?? ''),
    queryFn: () => api<DiscoveryNetworksResponse>(`/discovery/networks${langQs(lang)}`),
    staleTime: 24 * 60 * 60_000,
  });
}

// Disabled when q empty / <2 chars so SearchBar (515) doesn't fire on stray keystrokes.
export function useDiscoverySearch(q: string, enabled = true, lang?: string): ListResult {
  const trimmed = q.trim();
  const eff = enabled && trimmed.length >= 2;
  return useQuery<DiscoveryListResponse, ApiError>({
    queryKey: discoveryKeys.search(trimmed, lang ?? ''),
    queryFn: () => {
      const qs = new URLSearchParams({ q: trimmed });
      return api<DiscoveryListResponse>(`/discovery/search${withLang(qs, lang)}`);
    },
    enabled: eff,
    staleTime: 30_000,
  });
}

function buildDiscoverQs(f: DiscoveryFilter): URLSearchParams {
  const qs = new URLSearchParams();
  const join = (k: string, v?: readonly (string | number)[]) =>
    v?.length && qs.set(k, v.join(','));
  join('with_genres', f.with_genres);
  join('with_origin_country', f.with_origin_country);
  join('with_networks', f.with_networks);
  join('with_status', f.with_status);
  join('with_type', f.with_type);
  if (f.first_air_date_gte) qs.set('first_air_date_gte', f.first_air_date_gte);
  if (f.first_air_date_lte) qs.set('first_air_date_lte', f.first_air_date_lte);
  if (f.sort_by) qs.set('sort_by', f.sort_by);
  if (typeof f.vote_average_gte === 'number')
    qs.set('vote_average_gte', String(f.vote_average_gte));
  if (typeof f.vote_average_lte === 'number')
    qs.set('vote_average_lte', String(f.vote_average_lte));
  if (typeof f.page === 'number') qs.set('page', String(f.page));
  return qs;
}

export function useDiscover(
  filter: DiscoveryFilter,
  lang?: string,
  enabled = true,
  refetchInterval: RefetchInterval = false,
): ListResult {
  return useQuery<DiscoveryListResponse, ApiError>({
    queryKey: discoveryKeys.discover(filter, lang ?? ''),
    queryFn: () => {
      const qs = buildDiscoverQs(filter);
      return api<DiscoveryListResponse>(`/discovery/discover${withLang(qs, lang)}`);
    },
    enabled,
    staleTime: 30_000,
    refetchInterval,
  });
}
