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

  it('builds the URL with instance and seriesId', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [], total_episode_count: 0 });
    const { result } = renderHook(
      () => useSeriesCast({ instance: 'alpha', seriesId: 42, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/instances/alpha/series/42/cast?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ cast: [], crew: [] });
    renderHook(
      () => useSeriesCast({ instance: 'alpha', seriesId: 42 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/instances/alpha/series/42/cast');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(
      () => useSeriesCast({ instance: 'alpha', seriesId: undefined }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key', () => {
    expect(seriesCastQueryKey('alpha', 42, 'en-US')).toEqual([
      'series-cast', 'alpha', 42, 'en-US',
    ]);
  });
});
