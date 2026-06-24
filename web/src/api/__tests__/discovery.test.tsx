import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useDiscoveryTrending, useDiscoveryPopular, useDiscoveryByGenre,
  useDiscoveryGenresList, useDiscoveryNetworksList,
  useDiscoverySearch, useDiscover, discoveryKeys,
  type DiscoveryFilter,
} from '../discovery';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

const wrap = () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) =>
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
};

const sample = {
  items: [{
    series_id: 31, tmdb_id: 1234, title: 'Rick and Morty', year: 2013,
    poster_path: '/abc.jpg', origin_countries: ['US'], genres: ['Animation'],
    in_library_instances: ['sonarr-alpha'],
  }],
  refreshed_at: '2026-06-23T22:00:00Z', cache_status: 'hit', degraded: [],
};

beforeEach(() => mockApi.mockReset());
const wait10 = () => new Promise((r) => setTimeout(r, 10));

async function expectUrl(hook: () => unknown, url: string) {
  mockApi.mockResolvedValueOnce(sample);
  renderHook(hook, { wrapper: wrap() });
  await waitFor(() => expect(mockApi).toHaveBeenCalled());
  expect(mockApi).toHaveBeenCalledWith(url);
}

describe('list hooks fire correct URLs', () => {
  it('useDiscoveryTrending with lang', () =>
    expectUrl(() => useDiscoveryTrending('en-US'), '/discovery/trending?lang=en-US'));
  it('useDiscoveryTrending no-lang', () =>
    expectUrl(() => useDiscoveryTrending(), '/discovery/trending'));
  it('useDiscoveryPopular', () =>
    expectUrl(() => useDiscoveryPopular('ru'), '/discovery/popular?lang=ru'));
  it('useDiscoveryByGenre', () =>
    expectUrl(() => useDiscoveryByGenre(18, 'en'), '/discovery/genre/18?lang=en'));
  it('useDiscoveryGenresList', () =>
    expectUrl(() => useDiscoveryGenresList(), '/discovery/genres'));
  it('useDiscoveryNetworksList', () =>
    expectUrl(() => useDiscoveryNetworksList(), '/discovery/networks'));
});

describe('useDiscoveryByGenre — guards', () => {
  it('does not fire when id is undefined or 0', async () => {
    renderHook(() => useDiscoveryByGenre(undefined), { wrapper: wrap() });
    renderHook(() => useDiscoveryByGenre(0), { wrapper: wrap() });
    await wait10();
    expect(mockApi).not.toHaveBeenCalled();
  });
});

describe('useDiscoverySearch', () => {
  it('disabled when q empty / <2 / whitespace / enabled=false', async () => {
    renderHook(() => useDiscoverySearch(''), { wrapper: wrap() });
    renderHook(() => useDiscoverySearch('a'), { wrapper: wrap() });
    renderHook(() => useDiscoverySearch('  '), { wrapper: wrap() });
    renderHook(() => useDiscoverySearch('rick', false), { wrapper: wrap() });
    await wait10();
    expect(mockApi).not.toHaveBeenCalled();
  });
  it('fires with q + lang once enabled', async () => {
    mockApi.mockResolvedValueOnce(sample);
    renderHook(() => useDiscoverySearch('rick', true, 'en'), { wrapper: wrap() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/discovery/search?q=rick&lang=en');
  });
});

describe('useDiscover', () => {
  it('disabled when enabled=false', async () => {
    renderHook(() => useDiscover({}, undefined, false), { wrapper: wrap() });
    await wait10();
    expect(mockApi).not.toHaveBeenCalled();
  });
  it('serialises arrays + scalars + lang; omits empty', async () => {
    mockApi.mockResolvedValueOnce(sample).mockResolvedValueOnce(sample);
    const filter: DiscoveryFilter = {
      with_genres: [18, 35], with_origin_country: ['US', 'GB'],
      first_air_date_gte: '2016-01-01', sort_by: 'popularity.desc',
      vote_average_gte: 7.5, page: 2,
    };
    renderHook(() => useDiscover(filter, 'en'), { wrapper: wrap() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    const url = mockApi.mock.calls[0]?.[0] as string;
    for (const frag of [
      '/discovery/discover?', 'with_genres=18%2C35', 'with_origin_country=US%2CGB',
      'first_air_date_gte=2016-01-01', 'sort_by=popularity.desc',
      'vote_average_gte=7.5', 'page=2', 'lang=en',
    ]) expect(url).toContain(frag);

    renderHook(() => useDiscover({}), { wrapper: wrap() });
    await waitFor(() => expect(mockApi).toHaveBeenCalledTimes(2));
    expect(mockApi).toHaveBeenLastCalledWith('/discovery/discover');
  });
});

describe('discoveryKeys', () => {
  it('returns stable readonly tuples', () => {
    expect(discoveryKeys.trending('en')).toEqual(['discovery', 'trending', 'en']);
    expect(discoveryKeys.popular('ru')).toEqual(['discovery', 'popular', 'ru']);
    expect(discoveryKeys.byGenre(18, 'en')).toEqual(['discovery', 'genre', 18, 'en']);
    expect(discoveryKeys.byNetwork(49, '')).toEqual(['discovery', 'network', 49, '']);
    expect(discoveryKeys.byKeyword(7, '')).toEqual(['discovery', 'keyword', 7, '']);
    expect(discoveryKeys.genresList()).toEqual(['discovery', 'genres-list']);
    expect(discoveryKeys.networksList()).toEqual(['discovery', 'networks-list']);
    expect(discoveryKeys.search('rick', 'en')).toEqual(['discovery', 'search', 'rick', 'en']);
  });
});
