import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';

export type SortDir = 'asc' | 'desc';

export interface TableSort {
  readonly sortKey: string | null;
  readonly dir: SortDir | null;
  readonly toggle: (key: string) => void;
}

/**
 * Three-click sort cycle backed by `useSearchParams`.
 * key→asc, key→desc, then unsorted (params removed). Switching to a different
 * key resets the cycle to asc.
 */
export function useTableSort(): TableSort {
  const [params, setParams] = useSearchParams();
  const sortKey = params.get('sort');
  const rawDir = params.get('dir');
  const dir: SortDir | null = rawDir === 'asc' || rawDir === 'desc' ? rawDir : null;

  const toggle = useCallback(
    (key: string) => {
      const next = new URLSearchParams(params);
      if (sortKey !== key) {
        next.set('sort', key);
        next.set('dir', 'asc');
      } else if (dir === 'asc') {
        next.set('sort', key);
        next.set('dir', 'desc');
      } else {
        next.delete('sort');
        next.delete('dir');
      }
      setParams(next, { replace: true });
    },
    [params, setParams, sortKey, dir],
  );

  return useMemo(
    () => ({ sortKey: sortKey || null, dir: sortKey ? dir : null, toggle }),
    [sortKey, dir, toggle],
  );
}

export type Comparator<T> = (a: T, b: T) => number;

export function applySort<T>(
  rows: readonly T[],
  comparators: Readonly<Record<string, Comparator<T>>>,
  sortKey: string | null,
  dir: SortDir | null,
): readonly T[] {
  if (!sortKey || !dir) return rows;
  const cmp = comparators[sortKey];
  if (!cmp) return rows;
  const copy = rows.slice();
  copy.sort(cmp);
  if (dir === 'desc') copy.reverse();
  return copy;
}

/** Comparator helpers — null/undefined sort last in ascending order. */
export function cmpString<T>(get: (x: T) => string | null | undefined): Comparator<T> {
  return (a, b) => {
    const av = get(a);
    const bv = get(b);
    if (av == null && bv == null) return 0;
    if (av == null) return 1;
    if (bv == null) return -1;
    return av.localeCompare(bv);
  };
}

export function cmpNumber<T>(get: (x: T) => number | null | undefined): Comparator<T> {
  return (a, b) => {
    const av = get(a);
    const bv = get(b);
    if (av == null && bv == null) return 0;
    if (av == null) return 1;
    if (bv == null) return -1;
    return av - bv;
  };
}

export function cmpDate<T>(get: (x: T) => string | null | undefined): Comparator<T> {
  return (a, b) => {
    const av = get(a);
    const bv = get(b);
    const at = av ? Date.parse(av) : NaN;
    const bt = bv ? Date.parse(bv) : NaN;
    const aBad = Number.isNaN(at);
    const bBad = Number.isNaN(bt);
    if (aBad && bBad) return 0;
    if (aBad) return 1;
    if (bBad) return -1;
    return at - bt;
  };
}
