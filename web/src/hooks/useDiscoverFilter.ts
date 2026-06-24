import { useCallback, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import type { DiscoveryFilter } from '@/api/discovery';

// Story 516 / N-3d: URL <-> DiscoveryFilter sync. Keeps the Filter
// tab deep-linkable (and lets `setFilter` MERGE — never replace —
// so sliders/dropdowns don't clobber each other). `tab=filtered`
// and any other unrelated params are preserved on every write.

// Keys we manage. Anything else in URLSearchParams stays as-is.
const FILTER_KEYS = [
  'with_genres',
  'with_networks',
  'with_origin_country',
  'with_status',
  'with_type',
  'first_air_date_gte',
  'first_air_date_lte',
  'vote_average_gte',
  'vote_average_lte',
  'sort_by',
  'page',
] as const;

const NUM_LIST_KEYS = ['with_genres', 'with_networks'] as const;
const STR_LIST_KEYS = ['with_origin_country', 'with_status', 'with_type'] as const;

function splitCsv(raw: string): readonly string[] {
  return raw.split(',').map((s) => s.trim()).filter((s) => s.length > 0);
}

export function filterFromSearchParams(p: URLSearchParams): DiscoveryFilter {
  const out: Record<string, unknown> = {};
  for (const k of NUM_LIST_KEYS) {
    const raw = p.get(k);
    if (!raw) continue;
    const nums = splitCsv(raw)
      .map((s) => Number(s))
      .filter((n) => Number.isFinite(n));
    if (nums.length > 0) out[k] = nums;
  }
  for (const k of STR_LIST_KEYS) {
    const raw = p.get(k);
    if (!raw) continue;
    const arr = splitCsv(raw);
    if (arr.length > 0) out[k] = arr;
  }
  const gte = p.get('first_air_date_gte');
  if (gte) out['first_air_date_gte'] = gte;
  const lte = p.get('first_air_date_lte');
  if (lte) out['first_air_date_lte'] = lte;
  const sort = p.get('sort_by');
  if (sort) out['sort_by'] = sort;
  const vGte = p.get('vote_average_gte');
  if (vGte !== null && vGte !== '' && Number.isFinite(Number(vGte))) {
    out['vote_average_gte'] = Number(vGte);
  }
  const vLte = p.get('vote_average_lte');
  if (vLte !== null && vLte !== '' && Number.isFinite(Number(vLte))) {
    out['vote_average_lte'] = Number(vLte);
  }
  const page = p.get('page');
  if (page !== null && page !== '' && Number.isFinite(Number(page))) {
    out['page'] = Number(page);
  }
  return out as DiscoveryFilter;
}

export function searchParamsFromFilter(f: DiscoveryFilter): URLSearchParams {
  const qs = new URLSearchParams();
  const join = (k: string, v?: readonly (string | number)[]) => {
    if (v && v.length > 0) qs.set(k, v.join(','));
  };
  join('with_genres', f.with_genres);
  join('with_networks', f.with_networks);
  join('with_origin_country', f.with_origin_country);
  join('with_status', f.with_status);
  join('with_type', f.with_type);
  if (f.first_air_date_gte) qs.set('first_air_date_gte', f.first_air_date_gte);
  if (f.first_air_date_lte) qs.set('first_air_date_lte', f.first_air_date_lte);
  if (f.sort_by) qs.set('sort_by', f.sort_by);
  if (typeof f.vote_average_gte === 'number') {
    qs.set('vote_average_gte', String(f.vote_average_gte));
  }
  if (typeof f.vote_average_lte === 'number') {
    qs.set('vote_average_lte', String(f.vote_average_lte));
  }
  if (typeof f.page === 'number') qs.set('page', String(f.page));
  return qs;
}

// Patch type permits explicit `undefined` so callers can clear a field.
export type DiscoveryFilterPatch = {
  [K in keyof DiscoveryFilter]?: DiscoveryFilter[K] | undefined;
};

export interface UseDiscoverFilterResult {
  readonly filter: DiscoveryFilter;
  readonly setFilter: (patch: DiscoveryFilterPatch) => void;
  readonly clearFilter: () => void;
  readonly hasActiveFilter: boolean;
}

function isEmptyValue(v: unknown): boolean {
  if (v === undefined || v === null || v === '') return true;
  if (Array.isArray(v)) return v.length === 0;
  return false;
}

export function useDiscoverFilter(): UseDiscoverFilterResult {
  const [params, setParams] = useSearchParams();

  const filter = useMemo(() => filterFromSearchParams(params), [params]);

  const hasActiveFilter = useMemo(
    () => Object.values(filter).some((v) => !isEmptyValue(v)),
    [filter],
  );

  const setFilter = useCallback(
    (patch: DiscoveryFilterPatch) => {
      // Merge current filter + patch — drop fields whose patched value
      // is undefined/null/empty so the URL stays minimal.
      const merged: Record<string, unknown> = { ...filter };
      for (const [k, v] of Object.entries(patch)) {
        if (isEmptyValue(v)) delete merged[k];
        else merged[k] = v;
      }
      const next = new URLSearchParams(params);
      for (const k of FILTER_KEYS) next.delete(k);
      const fresh = searchParamsFromFilter(merged as DiscoveryFilter);
      fresh.forEach((value, key) => next.set(key, value));
      setParams(next, { replace: true });
    },
    [filter, params, setParams],
  );

  const clearFilter = useCallback(() => {
    const next = new URLSearchParams(params);
    for (const k of FILTER_KEYS) next.delete(k);
    setParams(next, { replace: true });
  }, [params, setParams]);

  return { filter, setFilter, clearFilter, hasActiveFilter };
}
