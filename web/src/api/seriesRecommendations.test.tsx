import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesRecommendations, seriesRecommendationsQueryKey } from './seriesRecommendations';

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

describe('useSeriesRecommendations', () => {
  beforeEach(() => mockApi.mockReset());

  it('exposes a stable query key including lang', () => {
    expect(seriesRecommendationsQueryKey(140, 20, 0, 'ru-RU')).toEqual([
      'series-recommendations', 140, 20, 0, 'ru-RU',
    ]);
    expect(seriesRecommendationsQueryKey(140, 20, 0, '')).toEqual([
      'series-recommendations', 140, 20, 0, '',
    ]);
  });

  it('fetches /series/:id/recommendations with default page when enabled', async () => {
    mockApi.mockResolvedValueOnce({
      instance: 'alpha', sonarr_series_id: 1, series_id: 140,
      items: [], total_count: 0, has_more: false, limit: 20, offset: 0, degraded: [],
    });
    const { result } = renderHook(
      () => useSeriesRecommendations({ seriesId: 140, enabled: true }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/140/recommendations?limit=20&offset=0');
  });

  it('honours custom limit/offset', async () => {
    mockApi.mockResolvedValueOnce({ items: [], total_count: 0, has_more: false, limit: 8, offset: 16, degraded: [] });
    renderHook(
      () => useSeriesRecommendations({ seriesId: 42, limit: 8, offset: 16, enabled: true }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42/recommendations?limit=8&offset=16');
  });

  it('appends &lang=ru-RU when lang is passed', async () => {
    mockApi.mockResolvedValueOnce({ items: [], total_count: 0, has_more: false, limit: 20, offset: 0, degraded: [] });
    renderHook(
      () => useSeriesRecommendations({ seriesId: 140, lang: 'ru-RU', enabled: true }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/140/recommendations?limit=20&offset=0&lang=ru-RU');
  });

  it('isolates cache per language via queryKey', () => {
    const ruKey = seriesRecommendationsQueryKey(140, 20, 0, 'ru-RU');
    const enKey = seriesRecommendationsQueryKey(140, 20, 0, 'en-US');
    expect(ruKey).not.toEqual(enKey);
  });

  it('does NOT fetch when enabled=false', () => {
    renderHook(
      () => useSeriesRecommendations({ seriesId: 42, enabled: false }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('does NOT fetch when seriesId is missing', () => {
    renderHook(
      () => useSeriesRecommendations({ seriesId: undefined, enabled: true }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });
});
