import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createDegradedPollInterval } from '@/hooks/useDegradedPollInterval';
import {
  useSeriesRecommendations,
  seriesRecommendationsQueryKey,
  isHotDegraded,
  recommendationsPollConfig,
  type SeriesRecommendationsResponse,
} from './seriesRecommendations';

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

function resp(degraded: string[]): SeriesRecommendationsResponse {
  return {
    instance: 'alpha',
    sonarr_series_id: 1,
    series_id: 140,
    items: [],
    total_count: 0,
    has_more: false,
    limit: 20,
    offset: 0,
    degraded,
  } as unknown as SeriesRecommendationsResponse;
}

describe('isHotDegraded (REC-2 media_cold re-poll)', () => {
  it('is true when degraded contains the REC-1 media_cold tag', () => {
    expect(isHotDegraded(resp(['media_cold']))).toBe(true);
  });

  it('is true when degraded contains the legacy tmdb_series tag', () => {
    expect(isHotDegraded(resp(['tmdb_series']))).toBe(true);
  });

  it('is false when degraded carries only non-hot tags', () => {
    expect(isHotDegraded(resp(['sonarr_queue']))).toBe(false);
  });

  it('is false for empty degraded and for an undefined response', () => {
    expect(isHotDegraded(resp([]))).toBe(false);
    expect(isHotDegraded(undefined)).toBe(false);
  });
});

// HARDEN-1: a stuck media_cold poster (blob never downloads) must not poll
// forever. Drive the hook's exact config via the non-React factory.
describe('recommendations degraded poll cap', () => {
  const hot = resp(['media_cold']); // length 1, hot

  it('stops after 6 ticks at a stable degraded length', () => {
    const poll = createDegradedPollInterval(recommendationsPollConfig(true));
    for (let i = 0; i < 6; i += 1) expect(poll(hot)).toBe(4_000);
    expect(poll(hot)).toBe(false);
  });

  it('resets the counter when degraded length changes (poll resumes)', () => {
    const poll = createDegradedPollInterval(recommendationsPollConfig(true));
    for (let i = 0; i < 6; i += 1) poll(hot);
    expect(poll(hot)).toBe(false); // capped
    expect(poll(resp(['media_cold', 'tmdb_series']))).toBe(4_000); // length 2 → resume
  });

  it('never polls when pollWhileDegraded is false', () => {
    const poll = createDegradedPollInterval(recommendationsPollConfig(false));
    expect(poll(hot)).toBe(false);
  });

  it('does not poll when the response is not hot-degraded', () => {
    const poll = createDegradedPollInterval(recommendationsPollConfig(true));
    expect(poll(resp(['sonarr_queue']))).toBe(false);
    expect(poll(resp([]))).toBe(false);
  });
});
