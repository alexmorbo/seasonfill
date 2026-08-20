import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  followSeries,
  unfollowSeries,
  useFollowedIds,
  followMovie,
  unfollowMovie,
  useFollowedMovieIds,
  type FollowListResponse,
  type FollowedMovieListResponse,
} from './follow';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (...args: unknown[]) => mockApi(...args),
  };
});

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('follow api client', () => {
  beforeEach(() => mockApi.mockReset());

  it('followSeries POSTs /follow with the series_id body', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await followSeries(140);
    expect(mockApi).toHaveBeenCalledWith('/follow', {
      method: 'POST',
      body: { series_id: 140 },
    });
  });

  it('unfollowSeries DELETEs /follow/:id', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await unfollowSeries(140);
    expect(mockApi).toHaveBeenCalledWith('/follow/140', { method: 'DELETE' });
  });

  it('useFollowedIds derives a Set of followed series_ids', async () => {
    const resp: FollowListResponse = {
      items: [
        { series_id: 31, title: 'A' },
        { series_id: 1294, title: 'B' },
      ],
    };
    mockApi.mockResolvedValueOnce(resp);
    const { result } = renderHook(() => useFollowedIds('en-US'), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.size).toBe(2));
    expect(result.current.has(31)).toBe(true);
    expect(result.current.has(1294)).toBe(true);
    expect(result.current.has(999)).toBe(false);
  });

  it('useFollowedIds is empty for an empty watchlist', async () => {
    mockApi.mockResolvedValueOnce({ items: [] } satisfies FollowListResponse);
    const { result } = renderHook(() => useFollowedIds('en-US'), {
      wrapper: wrapper(),
    });
    // starts empty, resolves to empty
    expect(result.current.size).toBe(0);
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(result.current.size).toBe(0);
  });
});

describe('movie follow api client', () => {
  beforeEach(() => mockApi.mockReset());

  it('followMovie POSTs /follow/movies with the tmdb_id body', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await followMovie(550);
    expect(mockApi).toHaveBeenCalledWith('/follow/movies', {
      method: 'POST',
      body: { tmdb_id: 550 },
    });
  });

  it('unfollowMovie DELETEs /follow/movies/:tmdb_id', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await unfollowMovie(550);
    expect(mockApi).toHaveBeenCalledWith('/follow/movies/550', { method: 'DELETE' });
  });

  it('useFollowedMovieIds derives a Set of followed tmdb_ids', async () => {
    const resp: FollowedMovieListResponse = {
      items: [
        { tmdb_id: 550, title: 'Fight Club' },
        { tmdb_id: 27205, title: 'Inception' },
      ],
    };
    mockApi.mockResolvedValueOnce(resp);
    const { result } = renderHook(() => useFollowedMovieIds('en-US'), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.size).toBe(2));
    expect(result.current.has(550)).toBe(true);
    expect(result.current.has(27205)).toBe(true);
    expect(result.current.has(999)).toBe(false);
  });

  it('useFollowedMovieIds is empty for an empty watchlist', async () => {
    mockApi.mockResolvedValueOnce({ items: [] } satisfies FollowedMovieListResponse);
    const { result } = renderHook(() => useFollowedMovieIds('en-US'), {
      wrapper: wrapper(),
    });
    // starts empty, resolves to empty
    expect(result.current.size).toBe(0);
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(result.current.size).toBe(0);
  });
});
