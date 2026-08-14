import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import {
  useMovieRecommendations,
  movieRecommendationsQueryKey,
} from './movieRecommendations';

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

const okResp = {
  tmdb_id: 603,
  items: [{ tmdb_id: 604, title: 'The Matrix Reloaded', year: 2003, tmdb_rating: 7.2, poster_asset: 'aaa' }],
  total_count: 1,
  has_more: false,
  limit: 20,
  offset: 0,
  degraded: [],
};

describe('useMovieRecommendations', () => {
  beforeEach(() => mockApi.mockReset());

  it('exposes a stable query key including tmdb_id + lang', () => {
    expect(movieRecommendationsQueryKey(603, 20, 0, 'ru-RU')).toEqual([
      'movie-recommendations', 603, 20, 0, 'ru-RU',
    ]);
    expect(movieRecommendationsQueryKey(603, 20, 0, '')).toEqual([
      'movie-recommendations', 603, 20, 0, '',
    ]);
  });

  it('isolates cache per language via queryKey', () => {
    expect(movieRecommendationsQueryKey(603, 20, 0, 'ru-RU'))
      .not.toEqual(movieRecommendationsQueryKey(603, 20, 0, 'en-US'));
  });

  it('fetches /movies/:tmdb_id/recommendations with default page', async () => {
    mockApi.mockResolvedValueOnce(okResp);
    const { result } = renderHook(
      () => useMovieRecommendations({ tmdbId: 603 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/603/recommendations?limit=20&offset=0');
    expect(result.current.data?.items?.[0]?.tmdb_id).toBe(604);
  });

  it('honours custom limit/offset', async () => {
    mockApi.mockResolvedValueOnce({ ...okResp, items: [], limit: 8, offset: 16 });
    renderHook(
      () => useMovieRecommendations({ tmdbId: 42, limit: 8, offset: 16 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/movies/42/recommendations?limit=8&offset=16');
  });

  it('appends &lang=ru-RU when lang is passed', async () => {
    mockApi.mockResolvedValueOnce({ ...okResp, items: [] });
    renderHook(
      () => useMovieRecommendations({ tmdbId: 603, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/movies/603/recommendations?limit=20&offset=0&lang=ru-RU');
  });

  it('does NOT fetch when tmdbId is missing', () => {
    renderHook(() => useMovieRecommendations({ tmdbId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('does NOT fetch for a non-positive tmdbId', () => {
    renderHook(() => useMovieRecommendations({ tmdbId: 0 }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('does NOT fetch when enabled=false', () => {
    renderHook(() => useMovieRecommendations({ tmdbId: 603, enabled: false }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('surfaces an ApiError on the error path', async () => {
    mockApi.mockRejectedValueOnce(new ApiError(500, 'boom'));
    const { result } = renderHook(
      () => useMovieRecommendations({ tmdbId: 603 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.error?.status).toBe(500);
  });
});
