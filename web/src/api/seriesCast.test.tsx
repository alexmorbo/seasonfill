import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesCast, seriesCastQueryKey } from './seriesCast';

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

describe('useSeriesCast', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the URL with seriesId', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [], total_episode_count: 0 });
    const { result } = renderHook(
      () => useSeriesCast({ seriesId: 42, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/cast?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [] });
    renderHook(
      () => useSeriesCast({ seriesId: 42 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42/cast');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(
      () => useSeriesCast({ seriesId: undefined }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key including the limit', () => {
    expect(seriesCastQueryKey(42, 'en-US')).toEqual(['series-cast', 42, 'en-US', 0]);
    expect(seriesCastQueryKey(42, 'en-US', 8)).toEqual(['series-cast', 42, 'en-US', 8]);
  });

  it('appends &limit to the URL when a positive limit is given', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [], total_episode_count: 0 });
    const { result } = renderHook(
      () => useSeriesCast({ seriesId: 42, lang: 'en-US', limit: 8 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/cast?lang=en-US&limit=8');
  });

  it('omits limit from the URL when 0/undefined (full page)', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [] });
    renderHook(() => useSeriesCast({ seriesId: 42, lang: 'en-US', limit: 0 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42/cast?lang=en-US');
  });
});
