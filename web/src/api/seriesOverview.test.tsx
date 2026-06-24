import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesOverview, seriesOverviewQueryKey } from './seriesOverview';

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

describe('useSeriesOverview', () => {
  beforeEach(() => mockApi.mockReset());

  it('exposes a stable query key', () => {
    expect(seriesOverviewQueryKey(140, 'ru-RU')).toEqual([
      'series-overview', 140, 'ru-RU',
    ]);
  });

  it('fetches /series/:id/overview with lang', async () => {
    mockApi.mockResolvedValueOnce({
      instance: 'alpha',
      sonarr_series_id: 140,
      series_id: 12345,
      lang: 'ru-RU',
      overview: { overview: 'desc', language: 'ru-RU', keywords: [], awards: null },
      degraded: [],
    });
    const { result } = renderHook(
      () => useSeriesOverview({ seriesId: 140, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/140/overview?lang=ru-RU');
    expect(result.current.data?.overview?.overview).toBe('desc');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ overview: { overview: '', language: '', keywords: [] }, degraded: [] });
    renderHook(
      () => useSeriesOverview({ seriesId: 42 }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42/overview');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(
      () => useSeriesOverview({ seriesId: undefined }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });
});
