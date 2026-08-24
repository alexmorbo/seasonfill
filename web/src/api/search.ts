import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { api } from '@/lib/api';
import { mediaUrl } from '@/api/series';
import { toBcp47 } from '@/lib/locale';
import type { components } from '@/api/schema';

type SearchResponse = components['schemas']['rest.SearchResponse'];
type RawSeries = components['schemas']['rest.SearchSeriesItem'];
type RawMovie = components['schemas']['rest.SearchMovieItem'];
type RawPerson = components['schemas']['rest.SearchPersonItem'];
type RawCollection = components['schemas']['rest.SearchCollectionItem'];

export type SearchSource = 'library' | 'catalog';
export type SearchScope = 'library' | 'catalog';

export interface SeriesHit {
  readonly kind: 'series';
  readonly source: SearchSource;
  readonly id?: number | undefined;
  readonly tmdbId?: number | undefined;
  readonly title: string;
  readonly year?: number | undefined;
  readonly posterPath?: string | undefined;
}
export interface MovieHit {
  readonly kind: 'movie';
  readonly source: SearchSource;
  readonly tmdbId: number;
  readonly title: string;
  readonly year?: number | undefined;
  readonly posterPath?: string | undefined;
}
export interface PersonHit {
  readonly kind: 'person';
  readonly source: SearchSource;
  readonly tmdbId: number;
  readonly name: string;
  readonly knownFor?: string | undefined;
  readonly profilePath?: string | undefined;
}
export interface CollectionHit {
  readonly kind: 'collection';
  readonly source: SearchSource;
  readonly tmdbId: number;
  readonly name: string;
  readonly posterPath?: string | undefined;
}
export type SearchHit = SeriesHit | MovieHit | CollectionHit | PersonHit;

export interface SearchGroup {
  readonly series: readonly SeriesHit[];
  readonly movies: readonly MovieHit[];
  readonly collections: readonly CollectionHit[];
  readonly people: readonly PersonHit[];
}
export interface UnifiedSearchResult {
  readonly library: SearchGroup;
  readonly catalog: SearchGroup;
  readonly libraryLoading: boolean;
  readonly catalogSearching: boolean;
  readonly hasResults: boolean;
  readonly enabled: boolean;
}

export function resolveSearchPoster(hit: SearchHit): string | undefined {
  if (hit.kind === 'person') return mediaUrl(hit.profilePath);
  return mediaUrl(hit.posterPath);
}

function coerceSource(raw: string | undefined, fallback: SearchSource): SearchSource {
  if (raw === 'library') return 'library';
  if (raw === 'catalog') return 'catalog';
  return fallback;
}

const posInt = (n: unknown): number | undefined =>
  typeof n === 'number' && n > 0 ? n : undefined;

export function mapSeriesItems(
  items: readonly RawSeries[] | undefined,
  fallback: SearchSource,
): SeriesHit[] {
  const out: SeriesHit[] = [];
  for (const it of items ?? []) {
    const title = it.title?.trim();
    const id = posInt(it.id);
    const tmdbId = posInt(it.tmdb_id);
    if (!title || (id === undefined && tmdbId === undefined)) continue;
    out.push({
      kind: 'series',
      source: coerceSource(it.source, fallback),
      id,
      tmdbId,
      title,
      year: typeof it.year === 'number' ? it.year : undefined,
      posterPath: it.poster_path || undefined,
    });
  }
  return out;
}

export function mapMovieItems(
  items: readonly RawMovie[] | undefined,
  fallback: SearchSource,
): MovieHit[] {
  const out: MovieHit[] = [];
  for (const it of items ?? []) {
    const title = it.title?.trim();
    const tmdbId = posInt(it.tmdb_id);
    if (!title || tmdbId === undefined) continue;
    out.push({
      kind: 'movie',
      source: coerceSource(it.source, fallback),
      tmdbId,
      title,
      year: typeof it.year === 'number' ? it.year : undefined,
      posterPath: it.poster_path || undefined,
    });
  }
  return out;
}

export function mapCollectionItems(
  items: readonly RawCollection[] | undefined,
  fallback: SearchSource,
): CollectionHit[] {
  const out: CollectionHit[] = [];
  for (const it of items ?? []) {
    const name = it.name?.trim();
    const tmdbId = posInt(it.tmdb_id);
    if (!name || tmdbId === undefined) continue;
    out.push({
      kind: 'collection',
      source: coerceSource(it.source, fallback),
      tmdbId,
      name,
      posterPath: it.poster_path || undefined,
    });
  }
  return out;
}

export function mapPersonItems(
  items: readonly RawPerson[] | undefined,
  fallback: SearchSource,
): PersonHit[] {
  const out: PersonHit[] = [];
  for (const it of items ?? []) {
    const name = it.name?.trim();
    const tmdbId = posInt(it.tmdb_id);
    if (!name || tmdbId === undefined) continue;
    out.push({
      kind: 'person',
      source: coerceSource(it.source, fallback),
      tmdbId,
      name,
      knownFor: it.known_for?.trim() || undefined,
      profilePath: it.profile_path || undefined,
    });
  }
  return out;
}

export const searchKeys = {
  all: ['search'] as const,
  scoped: (scope: SearchScope, q: string, lang: string) =>
    ['search', scope, q, lang] as const,
};

function fetchSearch(
  q: string,
  scope: SearchScope,
  lang?: string,
): Promise<SearchResponse> {
  const qs = new URLSearchParams({ q, scope });
  if (lang) qs.set('lang', lang);
  return api<SearchResponse>(`/search?${qs.toString()}`);
}

const groupHasHits = (g: SearchGroup): boolean =>
  g.series.length > 0 ||
  g.movies.length > 0 ||
  g.collections.length > 0 ||
  g.people.length > 0;

export function useUnifiedSearch(debouncedQuery: string): UnifiedSearchResult {
  const { i18n } = useTranslation();
  const lang = toBcp47(i18n.resolvedLanguage);
  const q = debouncedQuery.trim();

  const libraryEnabled = q.length >= 2;
  const libraryQuery = useQuery({
    queryKey: searchKeys.scoped('library', q, lang ?? ''),
    queryFn: () => fetchSearch(q, 'library', lang),
    enabled: libraryEnabled,
    staleTime: 30_000,
  });

  const librarySettled = libraryQuery.isSuccess || libraryQuery.isError;
  const catalogEnabled = q.length >= 3 && librarySettled;
  const catalogQuery = useQuery({
    queryKey: searchKeys.scoped('catalog', q, lang ?? ''),
    queryFn: () => fetchSearch(q, 'catalog', lang),
    enabled: catalogEnabled,
    staleTime: 60_000,
  });

  const library = useMemo<SearchGroup>(
    () => ({
      series: mapSeriesItems(libraryQuery.data?.series, 'library'),
      movies: mapMovieItems(libraryQuery.data?.movies, 'library'),
      collections: mapCollectionItems(libraryQuery.data?.collections, 'library'),
      people: mapPersonItems(libraryQuery.data?.people, 'library'),
    }),
    [libraryQuery.data],
  );

  const catalog = useMemo<SearchGroup>(() => {
    const libSeries = new Set(
      library.series.map((h) => h.tmdbId).filter((x): x is number => x !== undefined),
    );
    const libMovies = new Set(library.movies.map((h) => h.tmdbId));
    const libCollections = new Set(library.collections.map((h) => h.tmdbId));
    const libPeople = new Set(library.people.map((h) => h.tmdbId));
    return {
      series: mapSeriesItems(catalogQuery.data?.series, 'catalog').filter(
        (h) => h.tmdbId === undefined || !libSeries.has(h.tmdbId),
      ),
      movies: mapMovieItems(catalogQuery.data?.movies, 'catalog').filter(
        (h) => !libMovies.has(h.tmdbId),
      ),
      collections: mapCollectionItems(catalogQuery.data?.collections, 'catalog').filter(
        (h) => !libCollections.has(h.tmdbId),
      ),
      people: mapPersonItems(catalogQuery.data?.people, 'catalog').filter(
        (h) => !libPeople.has(h.tmdbId),
      ),
    };
  }, [catalogQuery.data, library]);

  return {
    library,
    catalog,
    libraryLoading: libraryEnabled && libraryQuery.isFetching,
    catalogSearching: catalogEnabled && catalogQuery.isFetching,
    hasResults: groupHasHits(library) || groupHasHits(catalog),
    enabled: libraryEnabled,
  };
}
