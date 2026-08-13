import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import {
  useMovie,
  useMoviesLibrary,
  type MovieDetail,
  type MovieLibraryList,
} from './movies';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (...args: unknown[]) => mockApi(...args) };
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

beforeEach(() => mockApi.mockReset());

describe('useMovie', () => {
  it('fetches /movies/:id with the lang query and returns the detail', async () => {
    const detail: MovieDetail = { tmdb_id: 438631, title: 'Dune', year: 2021 };
    mockApi.mockResolvedValueOnce(detail);
    const { result } = renderHook(() => useMovie(438631, 'en-US'), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/438631?lang=en-US');
    expect(result.current.data?.title).toBe('Dune');
  });

  it('surfaces a 404 as an error', async () => {
    mockApi.mockRejectedValueOnce(new ApiError(404, 'not found'));
    const { result } = renderHook(() => useMovie(999999), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.status).toBe(404);
  });

  it('does not fetch for a non-positive tmdbId', () => {
    renderHook(() => useMovie(0), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });
});

describe('useMoviesLibrary', () => {
  it('builds the querystring from every param and parses the envelope', async () => {
    const list: MovieLibraryList = {
      items: [{ tmdb_id: 1, title: 'A', instances: [] }],
      total: 1,
      has_more: false,
    };
    mockApi.mockResolvedValueOnce(list);
    const { result } = renderHook(
      () =>
        useMoviesLibrary({
          state: 'downloaded',
          sort: 'title_asc',
          q: 'dune',
          limit: 24,
          cursor: 24,
        }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith(
      '/movies?state=downloaded&sort=title_asc&q=dune&limit=24&cursor=24',
    );
    expect(result.current.data?.items?.[0]?.title).toBe('A');
  });

  it('omits absent params (no q, no cursor) from the querystring', async () => {
    mockApi.mockResolvedValueOnce({ items: [], total: 0, has_more: false });
    const { result } = renderHook(
      () => useMoviesLibrary({ state: 'all', sort: 'updated_desc', limit: 24 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith(
      '/movies?state=all&sort=updated_desc&limit=24',
    );
  });

  it('appends ?lang when provided', async () => {
    mockApi.mockResolvedValueOnce({ items: [], total: 0, has_more: false });
    const { result } = renderHook(
      () => useMoviesLibrary({ state: 'all', sort: 'updated_desc', limit: 24, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith(
      '/movies?state=all&sort=updated_desc&limit=24&lang=ru-RU',
    );
  });
});
