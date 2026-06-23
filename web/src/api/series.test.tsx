import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeries, seriesQueryKey } from './series';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...actual, api: (path: string) => mockApi(path) };
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('useSeries', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the global URL with seriesId and lang', async () => {
    mockApi.mockResolvedValueOnce({ id: 42, hero: { title: 'Severance' } });
    const { result } = renderHook(
      () => useSeries({ seriesId: 42, lang: 'en-US' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42?lang=en-US');
  });

  it('omits the lang query string when none provided', async () => {
    mockApi.mockResolvedValueOnce({ id: 42 });
    renderHook(() => useSeries({ seriesId: 42 }), { wrapper: wrapper() });
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(mockApi).toHaveBeenCalledWith('/series/42');
  });

  it('disables the query when seriesId is missing', () => {
    renderHook(() => useSeries({ seriesId: undefined }), { wrapper: wrapper() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key (no instance arg)', () => {
    expect(seriesQueryKey(42, 'en-US')).toEqual([
      'series-detail', 42, 'en-US',
    ]);
  });
});
