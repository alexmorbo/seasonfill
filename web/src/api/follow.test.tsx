import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  followSeries,
  unfollowSeries,
  useFollowedIds,
  type FollowListResponse,
} from './follow';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (...args: unknown[]) => mockApi(...args),
  };
});

vi.mock('sonner', () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

describe('follow api client', () => {
  beforeEach(() => mockApi.mockReset());

  it('followSeries POSTs /follow with the series_id body', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await followSeries(140);
    expect(mockApi).toHaveBeenCalledWith('/follow', {
      method: 'POST',
      body: { series_id: 140 },
    });
  });

  it('unfollowSeries DELETEs /follow/:id', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await unfollowSeries(140);
    expect(mockApi).toHaveBeenCalledWith('/follow/140', { method: 'DELETE' });
  });

  it('useFollowedIds derives a Set of followed series_ids', async () => {
    const resp: FollowListResponse = {
      items: [
        { series_id: 31, title: 'A' },
        { series_id: 1294, title: 'B' },
      ],
    };
    mockApi.mockResolvedValueOnce(resp);
    const { result } = renderHook(() => useFollowedIds('en-US'), {
      wrapper: wrapper(),
    });
    await waitFor(() => expect(result.current.size).toBe(2));
    expect(result.current.has(31)).toBe(true);
    expect(result.current.has(1294)).toBe(true);
    expect(result.current.has(999)).toBe(false);
  });

  it('useFollowedIds is empty for an empty watchlist', async () => {
    mockApi.mockResolvedValueOnce({ items: [] } satisfies FollowListResponse);
    const { result } = renderHook(() => useFollowedIds('en-US'), {
      wrapper: wrapper(),
    });
    // starts empty, resolves to empty
    expect(result.current.size).toBe(0);
    await waitFor(() => expect(mockApi).toHaveBeenCalled());
    expect(result.current.size).toBe(0);
  });
});
