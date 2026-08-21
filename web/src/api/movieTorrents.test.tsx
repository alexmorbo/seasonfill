import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useMovieTorrents, movieTorrentsQueryKey } from './movieTorrents';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrap() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useMovieTorrents', () => {
  beforeEach(() => mockApi.mockReset());

  it('does not fetch when disabled', () => {
    renderHook(() => useMovieTorrents({ tmdbId: 438631, visible: true, enabled: false }), { wrapper: wrap() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('builds the global URL with tmdbId', async () => {
    mockApi.mockResolvedValueOnce({ torrents: [] });
    const { result } = renderHook(
      () => useMovieTorrents({ tmdbId: 438631, visible: true }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/movies/438631/torrents');
  });

  it('has a stable query key', () => {
    expect(movieTorrentsQueryKey(438631)).toEqual(['movie-torrents', 438631]);
  });
});
