import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
  useDiscoveryRows, useRowDiscover, todayISO,
  reorderRows, useSaveDiscoveryRows, useResetDiscoveryRows,
} from './discoveryRows';

const mockApi = vi.fn();
vi.mock('@/lib/api', async () => {
  const a = await vi.importActual<typeof import('@/lib/api')>('@/lib/api');
  return { ...a, api: (p: string, init?: unknown) => mockApi(p, init) };
});

function wrapper() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } },
  });
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

beforeEach(() => mockApi.mockReset());

describe('todayISO', () => {
  it('returns a YYYY-MM-DD string', () => {
    expect(todayISO()).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });
});

describe('useDiscoveryRows', () => {
  it('GETs /discovery/rows', async () => {
    mockApi.mockResolvedValueOnce({ rows: [] });
    const { result } = renderHook(() => useDiscoveryRows(), { wrapper: wrapper() });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/rows', undefined);
  });
});

describe('useRowDiscover', () => {
  it('passes dotted params verbatim to /discovery/discover', async () => {
    mockApi.mockResolvedValueOnce({ items: [] });
    const params = { 'first_air_date.gte': '2026-08-10', sort_by: 'first_air_date.desc' };
    const { result } = renderHook(
      () => useRowDiscover(params, 'ru-RU', true),
      { wrapper: wrapper() },
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const called = (mockApi.mock.calls.at(0)?.[0] ?? '') as string;
    expect(called).toContain('/discovery/discover?');
    expect(called).toContain('first_air_date.gte=2026-08-10');
    expect(called).toContain('sort_by=first_air_date.desc');
    expect(called).toContain('lang=ru-RU');
    // Must NOT emit the underscore variant that buildDiscoverQs would.
    expect(called).not.toContain('first_air_date_gte');
  });

  it('does not fetch when disabled', () => {
    renderHook(() => useRowDiscover({ with_genres: '18' }, undefined, false), {
      wrapper: wrapper(),
    });
    expect(mockApi).not.toHaveBeenCalled();
  });
});

describe('reorderRows', () => {
  it('moves an element forward', () => {
    expect(reorderRows(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
  });
  it('moves an element backward', () => {
    expect(reorderRows(['a', 'b', 'c', 'd'], 3, 1)).toEqual(['a', 'd', 'b', 'c']);
  });
  it('is a no-op for equal indices', () => {
    expect(reorderRows(['a', 'b'], 1, 1)).toEqual(['a', 'b']);
  });
  it('is a no-op for out-of-range indices', () => {
    expect(reorderRows(['a', 'b'], 0, 5)).toEqual(['a', 'b']);
    expect(reorderRows(['a', 'b'], -1, 0)).toEqual(['a', 'b']);
  });
  it('returns a new array (no mutation)', () => {
    const src = ['a', 'b', 'c'];
    const out = reorderRows(src, 0, 2);
    expect(out).not.toBe(src);
    expect(src).toEqual(['a', 'b', 'c']);
  });
});

describe('useSaveDiscoveryRows', () => {
  it('PUTs /discovery/rows with the rows body', async () => {
    mockApi.mockResolvedValueOnce({ rows: [] });
    const { result } = renderHook(() => useSaveDiscoveryRows(), { wrapper: wrapper() });
    result.current.mutate([{
      row_type: 'genre', source: 'tmdb_discover', media_type: 'tv',
      params: { with_genres: '18' }, position: 0, enabled: true, title: 'Драмы',
    }]);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/rows', expect.objectContaining({
      method: 'PUT',
    }));
  });
});

describe('useResetDiscoveryRows', () => {
  it('DELETEs /discovery/rows', async () => {
    mockApi.mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useResetDiscoveryRows(), { wrapper: wrapper() });
    result.current.mutate();
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockApi).toHaveBeenCalledWith('/discovery/rows', expect.objectContaining({
      method: 'DELETE',
    }));
  });
});
