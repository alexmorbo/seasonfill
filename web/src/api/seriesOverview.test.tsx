import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createDegradedPollInterval } from '@/hooks/useDegradedPollInterval';
import {
  useSeriesOverview,
  seriesOverviewQueryKey,
  overviewPollConfig,
  type SeriesOverviewResponse,
} from './seriesOverview';

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

function ovResp(degraded: string[]): SeriesOverviewResponse {
  return { degraded } as unknown as SeriesOverviewResponse;
}

// HARDEN-1: the degraded poll must not run forever. Drive the exact config
// the hook uses via the non-React factory.
describe('overview degraded poll cap', () => {
  const hot = ovResp(['tmdb_series']); // length 1, hot

  it('stops after 6 ticks at a stable degraded length', () => {
    const poll = createDegradedPollInterval(overviewPollConfig(true));
    for (let i = 0; i < 6; i += 1) expect(poll(hot)).toBe(4_000);
    expect(poll(hot)).toBe(false);
  });

  it('resets the counter when degraded length changes (poll resumes)', () => {
    const poll = createDegradedPollInterval(overviewPollConfig(true));
    for (let i = 0; i < 6; i += 1) poll(hot);
    expect(poll(hot)).toBe(false); // capped
    expect(poll(ovResp(['tmdb_series', 'omdb']))).toBe(4_000); // length 2 → resume
  });

  it('never polls when pollWhileDegraded is false', () => {
    const poll = createDegradedPollInterval(overviewPollConfig(false));
    expect(poll(hot)).toBe(false);
  });

  it('does not poll when the response is not hot-degraded', () => {
    const poll = createDegradedPollInterval(overviewPollConfig(true));
    expect(poll(ovResp(['sonarr_queue']))).toBe(false);
    expect(poll(ovResp([]))).toBe(false);
  });
});
