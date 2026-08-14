import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ApiError } from '@/lib/api';
import { useMovieRatings, movieRatingsQueryKey } from './movieRatings';

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

describe('useMovieRatings', () => {
  beforeEach(() => mockApi.mockReset());

  it('fetches /movies/:tmdb_id/ratings with no lang query string', async () => {
    mockApi.mockResolvedValueOnce({ tmdb_rating: 8.7, sources: { tmdb: 'fresh' } });
    const { result } = renderHook(() => useMovieRatings({ tmdbId: 603 }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/603/ratings');
  });

  it('maps all six rating fields + sources block through to data', async () => {
    mockApi.mockResolvedValueOnce({
      tmdb_rating: 8.7,
      tmdb_votes: 24_000,
      imdb_rating: 8.7,
      imdb_votes: 1_900_000,
      rated: 'R',
      awards: 'Won 4 Oscars',
      sources: { tmdb: 'fresh', omdb: 'fresh' },
    });
    const { result } = renderHook(() => useMovieRatings({ tmdbId: 603 }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const d = result.current.data!;
    expect(d.tmdb_rating).toBe(8.7);
    expect(d.tmdb_votes).toBe(24_000);
    expect(d.imdb_rating).toBe(8.7);
    expect(d.imdb_votes).toBe(1_900_000);
    expect(d.rated).toBe('R');
    expect(d.awards).toBe('Won 4 Oscars');
    expect(d.sources).toEqual({ tmdb: 'fresh', omdb: 'fresh' });
  });

  it('tolerates an absent source (nullish/omitted fields) as success', async () => {
    // OMDb unavailable: no imdb_rating/votes/rated/awards, tmdb only.
    mockApi.mockResolvedValueOnce({
      tmdb_rating: 7.1,
      sources: { tmdb: 'fresh', omdb: 'unavailable' },
    });
    const { result } = renderHook(() => useMovieRatings({ tmdbId: 603 }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const d = result.current.data!;
    expect(d.tmdb_rating).toBe(7.1);
    expect(d.imdb_rating).toBeUndefined();
    expect(d.awards).toBeUndefined();
    expect(d.sources?.omdb).toBe('unavailable');
  });

  it('disables the query when tmdbId is missing', () => {
    renderHook(() => useMovieRatings({ tmdbId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('disables the query for a non-positive tmdbId', () => {
    renderHook(() => useMovieRatings({ tmdbId: 0 }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('surfaces an ApiError on the error path', async () => {
    mockApi.mockRejectedValueOnce(new ApiError(500, 'boom'));
    const { result } = renderHook(() => useMovieRatings({ tmdbId: 603 }), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.error?.status).toBe(500);
  });

  it('keys the cache on the tmdb_id alone (no lang)', () => {
    expect(movieRatingsQueryKey(603)).toEqual(['movie-ratings', 603]);
  });
});
