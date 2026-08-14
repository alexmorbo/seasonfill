import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useMovieOverview, movieOverviewQueryKey } from './movieOverview';

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

describe('useMovieOverview', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the URL with tmdbId + lang', async () => {
    mockApi.mockResolvedValueOnce({ title: 'The Matrix', tmdb_id: 603 });
    const { result } = renderHook(
      () => useMovieOverview({ tmdbId: 603, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/603/overview?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ title: 'The Matrix' });
    renderHook(() => useMovieOverview({ tmdbId: 603 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/movies/603/overview');
  });

  it('disables the query when tmdbId is missing', () => {
    renderHook(() => useMovieOverview({ tmdbId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('disables the query for a non-positive tmdbId', () => {
    renderHook(() => useMovieOverview({ tmdbId: 0 }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('folds lang into a stable query key', () => {
    expect(movieOverviewQueryKey(603, 'en-US')).toEqual(['movie-overview', 603, 'en-US']);
    expect(movieOverviewQueryKey(603, 'ru-RU')).toEqual(['movie-overview', 603, 'ru-RU']);
  });

  it('surfaces a degraded (missing_lang) response as success data', async () => {
    mockApi.mockResolvedValueOnce({
      title: 'The Matrix',
      overview: 'A hacker learns the truth.',
      degraded: ['missing_lang'],
      served_language: 'en-US',
      lang: 'ru-RU',
      tmdb_id: 603,
    });
    const { result } = renderHook(
      () => useMovieOverview({ tmdbId: 603, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.degraded).toEqual(['missing_lang']);
    expect(result.current.data?.served_language).toBe('en-US');
  });
});
