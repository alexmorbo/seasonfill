import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useMovieTrending, useMoviePopular, useMovieSearch, useMovieRowDiscover,
} from './discoveryMovies';

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
  items: [{ movie_id: 5, tmdb_id: 438631, title: 'Dune', year: 2021, tmdb_rating: 8.1 }],
  page: 1, per_page: 20, cache_status: 'hit',
};

beforeEach(() => mockApi.mockReset());

async function expectUrl(hook: () => unknown, url: string) {
  mockApi.mockResolvedValueOnce(sample);
  renderHook(hook, { wrapper: wrap() });
  await waitFor(() => expect(mockApi).toHaveBeenCalled());
  expect(mockApi).toHaveBeenCalledWith(url);
}

describe('movie discovery hooks fire the correct movie endpoints', () => {
  it('useMovieTrending default scope=day with lang', () =>
    expectUrl(() => useMovieTrending('en-US'),
      '/discovery/movie/trending?scope=day&lang=en-US'));

  it('useMovieTrending scope=week', () =>
    expectUrl(() => useMovieTrending('ru', 'week'),
      '/discovery/movie/trending?scope=week&lang=ru'));

  it('useMoviePopular with lang', () =>
    expectUrl(() => useMoviePopular('ru'), '/discovery/movie/popular?lang=ru'));

  it('useMoviePopular no lang → bare path', () =>
    expectUrl(() => useMoviePopular(), '/discovery/movie/popular'));

  it('useMovieSearch with lang', () =>
    expectUrl(() => useMovieSearch('dune', true, 'en'),
      '/discovery/movie/search?q=dune&lang=en'));

  it('useMovieRowDiscover passes dotted keys verbatim + lang last', () =>
    expectUrl(
      () => useMovieRowDiscover(
        { with_genres: '28', 'primary_release_date.gte': '2026-01-01' }, 'en', true),
      '/discovery/movie/discover?with_genres=28&primary_release_date.gte=2026-01-01&lang=en',
    ));
});

describe('movie discovery hook guards', () => {
  it('useMovieSearch does not fire below 2 chars', async () => {
    renderHook(() => useMovieSearch('d', true), { wrapper: wrap() });
    await new Promise((r) => setTimeout(r, 10));
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('useMovieRowDiscover does not fire when disabled', async () => {
    renderHook(
      () => useMovieRowDiscover({ with_genres: '28' }, 'en', false),
      { wrapper: wrap() },
    );
    await new Promise((r) => setTimeout(r, 10));
    expect(mockApi).not.toHaveBeenCalled();
  });
});
