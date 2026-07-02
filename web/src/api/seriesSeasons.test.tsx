import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesSeasons, seriesSeasonsQueryKey } from './seriesSeasons';

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

describe('useSeriesSeasons', () => {
  beforeEach(() => mockApi.mockReset());

  it('exposes a stable query key carrying lang verbatim', () => {
    expect(seriesSeasonsQueryKey(42, 'ru-RU')).toEqual(['series-seasons', 42, 'ru-RU']);
    expect(seriesSeasonsQueryKey(42, 'en-US')).toEqual(['series-seasons', 42, 'en-US']);
  });

  it('fetches /series/:id/seasons with lang in the URL', async () => {
    mockApi.mockResolvedValueOnce({
      series_id: 42,
      seasons: [{ season_number: 1, name: 'Сезон 1', episode_count: 10 }],
      synced_at: '2026-06-30T00:00:00Z',
    });
    const { result } = renderHook(
      () => useSeriesSeasons({ seriesId: 42, lang: 'ru-RU' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/seasons?lang=ru-RU');
    expect(result.current.data?.seasons?.[0]?.name).toBe('Сезон 1');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ series_id: 42, seasons: [], synced_at: '2026-06-30T00:00:00Z' });
    renderHook(() => useSeriesSeasons({ seriesId: 42 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42/seasons');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(() => useSeriesSeasons({ seriesId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('disables the query when seriesId is <= 0', () => {
    renderHook(() => useSeriesSeasons({ seriesId: 0 }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });
});
