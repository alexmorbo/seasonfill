import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesLibrary, seriesLibraryQueryKey } from './seriesLibrary';

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

const libraryResponse = {
  instance: 'homelab',
  library: { episodes_on_disk: 42, episodes_total: 48, missing_count: 6 },
  recent: [{ event_type: 'imported', subject: 'S05E02', at: new Date().toISOString() }],
};

describe('useSeriesLibrary', () => {
  beforeEach(() => mockApi.mockReset());

  it('builds the /library URL with the instance query param', async () => {
    mockApi.mockResolvedValueOnce(libraryResponse);
    const { result } = renderHook(
      () => useSeriesLibrary({ seriesId: 42, instance: 'homelab' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/library?instance=homelab');
  });

  it('returns library + recent on success', async () => {
    mockApi.mockResolvedValueOnce(libraryResponse);
    const { result } = renderHook(
      () => useSeriesLibrary({ seriesId: 42, instance: 'homelab' }),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.library?.episodes_on_disk).toBe(42);
    expect(result.current.data?.recent?.[0]?.subject).toBe('S05E02');
  });

  it('is disabled when instance is undefined (TMDB-only)', () => {
    const { result } = renderHook(
      () => useSeriesLibrary({ seriesId: 42, instance: undefined }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
    expect(result.current.data).toBeUndefined();
  });

  it('is disabled when seriesId is undefined', () => {
    renderHook(
      () => useSeriesLibrary({ seriesId: undefined, instance: 'homelab' }),
      { wrapper: wrapper() },
    );
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('exposes a stable query key', () => {
    expect(seriesLibraryQueryKey(42, 'homelab')).toEqual([
      'series-library', 42, 'homelab',
    ]);
  });
});
