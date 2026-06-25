import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import {
  useAddToSonarr,
  useDiscoveryTrending, useDiscoveryPopular, useDiscoveryByGenre,
  useDiscoveryGenresList, useDiscoveryNetworksList,
  useDiscoverySearch, useDiscover, discoveryKeys,
  type DiscoveryFilter,
} from '../discovery';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (path: string, init?: RequestInit) =>
      init === undefined ? mockApi(path) : mockApi(path, init),
  };
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
  it('useDiscoveryGenresList without lang → bare path', () =>
    expectUrl(() => useDiscoveryGenresList(), '/discovery/genres'));
  it('useDiscoveryGenresList with lang → ?lang= appended', () =>
    expectUrl(() => useDiscoveryGenresList('ru-RU'), '/discovery/genres?lang=ru-RU'));
  it('useDiscoveryNetworksList without lang → bare path', () =>
    expectUrl(() => useDiscoveryNetworksList(), '/discovery/networks'));
  it('useDiscoveryNetworksList with lang → ?lang= appended', () =>
    expectUrl(() => useDiscoveryNetworksList('ru-RU'), '/discovery/networks?lang=ru-RU'));
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
    expect(discoveryKeys.all).toEqual(['discovery']);
    expect(discoveryKeys.trending('en')).toEqual(['discovery', 'trending', 'en']);
    expect(discoveryKeys.popular('ru')).toEqual(['discovery', 'popular', 'ru']);
    expect(discoveryKeys.byGenre(18, 'en')).toEqual(['discovery', 'genre', 18, 'en']);
    expect(discoveryKeys.byNetwork(49, '')).toEqual(['discovery', 'network', 49, '']);
    expect(discoveryKeys.byKeyword(7, '')).toEqual(['discovery', 'keyword', 7, '']);
    expect(discoveryKeys.genresList('ru-RU')).toEqual(['discovery', 'genres-list', 'ru-RU']);
    expect(discoveryKeys.genresList('')).toEqual(['discovery', 'genres-list', '']);
    expect(discoveryKeys.networksList('ru-RU')).toEqual(['discovery', 'networks-list', 'ru-RU']);
    expect(discoveryKeys.search('rick', 'en')).toEqual(['discovery', 'search', 'rick', 'en']);
  });
});

describe('useAddToSonarr', () => {
  const successResp = {
    sonarr_series_id: 555, instance_name: 'main',
    user_tag_label: 'sf-alex', user_tag_id: 12,
  };
  const addBody = {
    instance_name: 'main', tvdb_id: 81189,
    quality_profile_id: 6, root_folder_path: '/tv',
    monitor_mode: 'all' as const,
  };

  it('POSTs the BE wire shape and returns the resolved tag', async () => {
    mockApi.mockResolvedValueOnce(successResp);
    const { result } = renderHook(() => useAddToSonarr(), { wrapper: wrap() });
    let returned: typeof successResp | undefined;
    await act(async () => {
      returned = await result.current.mutateAsync(addBody);
    });
    expect(mockApi).toHaveBeenCalledWith(
      '/discovery/add-to-sonarr',
      { method: 'POST', body: addBody },
    );
    expect(returned?.user_tag_label).toBe('sf-alex');
  });

  it('invalidates the entire discovery cache on success', async () => {
    mockApi.mockResolvedValueOnce(successResp);
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false, gcTime: 0 },
        mutations: { retry: false },
      },
    });
    // Spy on invalidateQueries — the observable contract is "the
    // mutation calls invalidateQueries with the discovery prefix".
    // We don't drive `isInvalidated` directly because an inactive
    // query (seeded via setQueryData, no observer) does not transition
    // to invalidated under all React Query versions; the spy verifies
    // the contract directly.
    const spy = vi.spyOn(qc, 'invalidateQueries');
    const wrapper = ({ children }: { children: React.ReactNode }) =>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useAddToSonarr(), { wrapper });
    await act(async () => { await result.current.mutateAsync(addBody); });
    expect(spy).toHaveBeenCalledWith({ queryKey: discoveryKeys.all });
  });

  it('surfaces the F-2c slug envelope as an ApiError on 502', async () => {
    mockApi.mockRejectedValueOnce(
      new ApiError(502, 'sonarr_unreachable',
        { error: 'sonarr_unreachable', message: 'unreachable' }),
    );
    const { result } = renderHook(() => useAddToSonarr(), { wrapper: wrap() });
    let captured: unknown;
    await act(async () => {
      try { await result.current.mutateAsync(addBody); }
      catch (e) { captured = e; }
    });
    expect(captured).toBeInstanceOf(ApiError);
    expect((captured as ApiError).status).toBe(502);
    expect(((captured as ApiError).body as { error?: string })?.error)
      .toBe('sonarr_unreachable');
  });
});
