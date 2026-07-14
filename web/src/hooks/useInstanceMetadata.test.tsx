// Story 522 / N-4e — verifies the React Query wrappers:
//   - enabled gating (empty / undefined instance name)
//   - URL routing
//   - invalidation after the refresh mutation

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  instanceMetadataKeys,
  useQualityProfiles,
  useRefreshInstanceMetadata,
  useRootFolders,
} from './useInstanceMetadata';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return {
    ...actual,
    api: (path: string, init?: RequestInit) =>
      init === undefined ? mockApi(path) : mockApi(path, init),
  };
});

const qpPayload = {
  items: [{ id: 1, name: 'HD-1080p' }],
  refreshed_at: 'Mon, 23 Jun 2026 22:00:00 GMT',
  cache_status: 'hit',
  instance_name: 'main',
};
const rfPayload = {
  items: [{ id: 7, path: '/tv', accessible: true, free_space: 100 }],
  refreshed_at: 'Mon, 23 Jun 2026 22:00:00 GMT',
  cache_status: 'hit',
  instance_name: 'main',
};

function mkClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity, staleTime: 0 },
      mutations: { retry: false },
    },
  });
}

function wrap(qc: QueryClient) {
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
}

beforeEach(() => mockApi.mockReset());
const wait10 = () => new Promise((r) => setTimeout(r, 10));

describe('useQualityProfiles', () => {
  it('disabled when name empty or undefined', async () => {
    const qc = mkClient();
    renderHook(() => useQualityProfiles(''), { wrapper: wrap(qc) });
    renderHook(() => useQualityProfiles(undefined), { wrapper: wrap(qc) });
    await wait10();
    expect(mockApi).not.toHaveBeenCalled();
  });

  it('fires once enabled and a name is provided', async () => {
    mockApi.mockResolvedValueOnce(qpPayload);
    const qc = mkClient();
    const { result } = renderHook(
      () => useQualityProfiles('main'),
      { wrapper: wrap(qc) },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/main/quality-profiles',
    );
    expect(result.current.data?.items[0]?.name).toBe('HD-1080p');
  });

  it('honors the explicit enabled=false', async () => {
    const qc = mkClient();
    renderHook(() => useQualityProfiles('main', false), { wrapper: wrap(qc) });
    await wait10();
    expect(mockApi).not.toHaveBeenCalled();
  });
});

describe('useRootFolders', () => {
  it('fires when enabled', async () => {
    mockApi.mockResolvedValueOnce(rfPayload);
    const qc = mkClient();
    const { result } = renderHook(
      () => useRootFolders('main'),
      { wrapper: wrap(qc) },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/instances/main/root-folders');
    expect(result.current.data?.items[0]?.path).toBe('/tv');
  });
});

describe('useRefreshInstanceMetadata', () => {
  it('invalidates QP+RF queries on success', async () => {
    mockApi.mockResolvedValueOnce({ invalidated: true });
    const qc = mkClient();
    // Seed both caches so we can observe the invalidation.
    qc.setQueryData(
      instanceMetadataKeys.qualityProfiles('main'),
      qpPayload,
    );
    qc.setQueryData(
      instanceMetadataKeys.rootFolders('main'),
      rfPayload,
    );
    const { result } = renderHook(
      () => useRefreshInstanceMetadata(),
      { wrapper: wrap(qc) },
    );
    await act(async () => {
      await result.current.mutateAsync('main');
    });
    expect(mockApi).toHaveBeenCalledWith(
      '/instances/main/refresh-metadata',
      { method: 'POST' },
    );
    expect(
      qc.getQueryState(instanceMetadataKeys.qualityProfiles('main'))
        ?.isInvalidated,
    ).toBe(true);
    expect(
      qc.getQueryState(instanceMetadataKeys.rootFolders('main'))
        ?.isInvalidated,
    ).toBe(true);
  });
});
