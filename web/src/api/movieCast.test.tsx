import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useMovieCast, movieCastQueryKey } from './movieCast';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useMovieCast', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the URL with tmdbId + lang and omits the default sort', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], degraded: [], tmdb_id: 603 });
    const { result } = renderHook(
      () => useMovieCast({ tmdbId: 603, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/603/cast?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ cast: [] });
    renderHook(() => useMovieCast({ tmdbId: 603 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/movies/603/cast');
  });

  it('threads the selected sort into the URL', async () => {
    mockApi.mockResolvedValueOnce({ cast: [] });
    const { result } = renderHook(
      () => useMovieCast({ tmdbId: 603, lang: 'en-US', sort: 'name' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/603/cast?lang=en-US&sort=name');
  });

  it('disables the query when tmdbId is missing', () => {
    renderHook(() => useMovieCast({ tmdbId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('disables the query for a non-positive tmdbId', () => {
    renderHook(() => useMovieCast({ tmdbId: 0 }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('folds lang + sort into a stable query key', () => {
    expect(movieCastQueryKey(603, 'en-US')).toEqual(['movie-cast', 603, 'en-US', 'credit']);
    expect(movieCastQueryKey(603, 'ru-RU', 'name')).toEqual(['movie-cast', 603, 'ru-RU', 'name']);
  });

  it('surfaces a degraded (missing_lang) response as success data', async () => {
    mockApi.mockResolvedValueOnce({
      cast: [{ name: 'Keanu Reeves', tmdb_id: 6384, credit_order: 0 }],
      degraded: ['missing_lang'],
      served_language: 'en-US',
      lang: 'ru-RU',
      tmdb_id: 603,
    });
    const { result } = renderHook(
      () => useMovieCast({ tmdbId: 603, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.degraded).toEqual(['missing_lang']);
    expect(result.current.data?.served_language).toBe('en-US');
  });
});
