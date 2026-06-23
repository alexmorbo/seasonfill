import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useSeriesTorrents, seriesTorrentsQueryKey } from './seriesTorrents';

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

describe('useSeriesTorrents', () => {
  beforeEach(() => mockApi.mockReset());

  it('does not fetch when disabled', () => {
    renderHook(() => useSeriesTorrents({ seriesId: 42, visible: true, enabled: false }), { wrapper: wrap() });
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('builds the global URL with seriesId', async () => {
    mockApi.mockResolvedValueOnce({ torrents: [] });
    const { result } = renderHook(
      () => useSeriesTorrents({ seriesId: 42, visible: true }),
      { wrapper: wrap() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/series/42/torrents');
  });

  it('has a stable query key', () => {
    expect(seriesTorrentsQueryKey(42)).toEqual(['series-torrents', 42]);
  });
});
