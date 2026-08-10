import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  addBlocklist, listBlocklist, deleteBlocklist, searchKeywords,
  useBlocklist, useKeywordSearch, useAddBlocklist, useDeleteBlocklist,
  blocklistKeys, type BlocklistRow,
} from './blocklist';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (...args: unknown[]) => mockApi(...args) };
});

function wrapper() {
  const qc = new QueryClient({
    // gcTime Infinity: setQueryData-seeded cache (no query observer mounted in
    // the delete tests) must survive until the optimistic assertions read it.
    defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: 0 } },
  });
  return {
    qc,
    Wrapper: ({ children }: { children: ReactNode }) =>
      createElement(QueryClientProvider, { client: qc }, children),
  };
}

beforeEach(() => mockApi.mockReset());

describe('blocklist client fns', () => {
  it('addBlocklist POSTs the body', async () => {
    mockApi.mockResolvedValueOnce({ id: 7, kind: 'tmdb', ref_id: 42 });
    const r = await addBlocklist({ kind: 'tmdb', ref_id: 42 });
    expect(r.id).toBe(7);
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist',
      expect.objectContaining({ method: 'POST', body: { kind: 'tmdb', ref_id: 42 } }));
  });

  it('listBlocklist GETs the bare array', async () => {
    const rows: BlocklistRow[] = [
      { id: 1, kind: 'tmdb', ref_id: 42, title: 'Severance', poster_hash: 'abc' },
      { id: 2, kind: 'keyword', ref_id: 99, label: 'anime' },
    ];
    mockApi.mockResolvedValueOnce(rows);
    await expect(listBlocklist()).resolves.toEqual(rows);
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist');
  });

  it('deleteBlocklist DELETEs by id', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    await deleteBlocklist(5);
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist/5',
      expect.objectContaining({ method: 'DELETE' }));
  });

  it('searchKeywords encodes the query', async () => {
    mockApi.mockResolvedValueOnce([{ id: 3, name: 'time travel' }]);
    await searchKeywords('time travel');
    const called = (mockApi.mock.calls.at(0)?.[0] ?? '') as string;
    expect(called).toContain('/discovery/keyword-search?q=');
    expect(called).toContain('time+travel');
  });
});

describe('useBlocklist', () => {
  it('GETs /discovery/blocklist', async () => {
    mockApi.mockResolvedValueOnce([]);
    const { Wrapper } = wrapper();
    const { result } = renderHook(() => useBlocklist(), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist');
  });
});

describe('useKeywordSearch', () => {
  it('does not fetch below 2 chars', () => {
    const { Wrapper } = wrapper();
    renderHook(() => useKeywordSearch('a', true), { wrapper: Wrapper });
    expect(mockApi).not.toHaveBeenCalled();
  });
  it('fetches at >=2 chars when enabled', async () => {
    mockApi.mockResolvedValueOnce([{ id: 1, name: 'anime' }]);
    const { Wrapper } = wrapper();
    const { result } = renderHook(() => useKeywordSearch('an', true), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const called = (mockApi.mock.calls.at(0)?.[0] ?? '') as string;
    expect(called).toContain('/discovery/keyword-search?q=an');
  });
});

describe('useAddBlocklist', () => {
  it('POSTs and invalidates the list', async () => {
    mockApi.mockResolvedValueOnce({ id: 9, kind: 'keyword', ref_id: 5, label: 'x' });
    const { Wrapper } = wrapper();
    const { result } = renderHook(() => useAddBlocklist(), { wrapper: Wrapper });
    result.current.mutate({ kind: 'keyword', ref_id: 5, label: 'x' });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist',
      expect.objectContaining({ method: 'POST' }));
  });
});

describe('useDeleteBlocklist', () => {
  it('optimistically removes the row then DELETEs', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    const { qc, Wrapper } = wrapper();
    qc.setQueryData<readonly BlocklistRow[]>(blocklistKeys.list, [
      { id: 1, kind: 'keyword', ref_id: 1, label: 'a' },
      { id: 2, kind: 'keyword', ref_id: 2, label: 'b' },
    ]);
    const { result } = renderHook(() => useDeleteBlocklist(), { wrapper: Wrapper });
    result.current.mutate(1);
    // optimistic slice applied synchronously in onMutate
    await waitFor(() => {
      const data = qc.getQueryData<readonly BlocklistRow[]>(blocklistKeys.list);
      expect(data?.some((r) => r.id === 1)).toBe(false);
    });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/blocklist/1',
      expect.objectContaining({ method: 'DELETE' }));
  });

  it('rolls back on error', async () => {
    mockApi.mockRejectedValueOnce(new Error('boom'));
    const { qc, Wrapper } = wrapper();
    const seed: readonly BlocklistRow[] = [{ id: 1, kind: 'keyword', ref_id: 1, label: 'a' }];
    qc.setQueryData(blocklistKeys.list, seed);
    const { result } = renderHook(() => useDeleteBlocklist(), { wrapper: Wrapper });
    result.current.mutate(1);
    await waitFor(() => expect(result.current.isError).toBe(true));
    const data = qc.getQueryData<readonly BlocklistRow[]>(blocklistKeys.list);
    expect(data?.some((r) => r.id === 1)).toBe(true);
  });
});
