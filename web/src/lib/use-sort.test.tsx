import { describe, expect, it } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { MemoryRouter, useSearchParams } from 'react-router-dom';
import type { ReactNode } from 'react';
import { applySort, cmpNumber, cmpString, useTableSort } from './use-sort';

function wrap(initial: string) {
  return ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[initial]}>{children}</MemoryRouter>
  );
}

describe('useTableSort', () => {
  it('cycles asc → desc → unsorted and writes URL params', () => {
    const probe = renderHook(
      () => {
        const sort = useTableSort();
        const [params] = useSearchParams();
        return { sort, params };
      },
      { wrapper: wrap('/x') },
    );

    expect(probe.result.current.sort.sortKey).toBeNull();
    expect(probe.result.current.sort.dir).toBeNull();

    act(() => probe.result.current.sort.toggle('name'));
    expect(probe.result.current.sort.sortKey).toBe('name');
    expect(probe.result.current.sort.dir).toBe('asc');
    expect(probe.result.current.params.get('sort')).toBe('name');
    expect(probe.result.current.params.get('dir')).toBe('asc');

    act(() => probe.result.current.sort.toggle('name'));
    expect(probe.result.current.sort.dir).toBe('desc');
    expect(probe.result.current.params.get('dir')).toBe('desc');

    act(() => probe.result.current.sort.toggle('name'));
    expect(probe.result.current.sort.sortKey).toBeNull();
    expect(probe.result.current.sort.dir).toBeNull();
    expect(probe.result.current.params.get('sort')).toBeNull();
    expect(probe.result.current.params.get('dir')).toBeNull();
  });

  it('switching to a different key resets to asc', () => {
    const probe = renderHook(() => useTableSort(), {
      wrapper: wrap('/x?sort=name&dir=desc'),
    });
    act(() => probe.result.current.toggle('age'));
    expect(probe.result.current.sortKey).toBe('age');
    expect(probe.result.current.dir).toBe('asc');
  });
});

describe('applySort', () => {
  it('returns rows unchanged when key or dir missing', () => {
    const rows = [{ x: 2 }, { x: 1 }];
    const cmps = { x: cmpNumber<{ x: number }>((r) => r.x) };
    expect(applySort(rows, cmps, null, null)).toBe(rows);
    expect(applySort(rows, cmps, 'x', null)).toBe(rows);
    expect(applySort(rows, cmps, null, 'asc')).toBe(rows);
  });
  it('sorts asc and desc and is stable on copy', () => {
    const rows = [{ s: 'b' }, { s: 'a' }];
    const cmps = { s: cmpString<{ s: string }>((r) => r.s) };
    expect(applySort(rows, cmps, 's', 'asc').map((r) => r.s)).toEqual(['a', 'b']);
    expect(applySort(rows, cmps, 's', 'desc').map((r) => r.s)).toEqual(['b', 'a']);
    // original untouched
    expect(rows.map((r) => r.s)).toEqual(['b', 'a']);
  });
});
