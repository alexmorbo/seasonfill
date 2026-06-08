import type { SeriesCacheStatus, SeriesCacheSort } from '@/lib/api/seriesCache';

export interface SeriesFiltersValue {
  readonly search: string;
  readonly state: SeriesCacheStatus;
  readonly sort: SeriesCacheSort;
  readonly monitoredOnly: boolean;
  readonly networks: ReadonlySet<string>;
}

export function isDefaultFilters(v: SeriesFiltersValue, d: SeriesFiltersValue): boolean {
  if (v.search !== d.search) return false;
  if (v.state !== d.state) return false;
  if (v.sort !== d.sort) return false;
  if (v.monitoredOnly !== d.monitoredOnly) return false;
  if (v.networks.size !== d.networks.size) return false;
  for (const n of v.networks) if (!d.networks.has(n)) return false;
  return true;
}
