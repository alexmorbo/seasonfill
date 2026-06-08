import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import {
  useSeriesCacheInfinite,
  flattenSeriesCachePages,
  type SeriesCacheStatus,
  type SeriesCacheSort,
  type SeriesCacheItem,
} from '@/lib/api/seriesCache';
import { SeriesHeader } from '@/components/series/SeriesHeader';
import { SeriesGrid } from '@/components/series/SeriesGrid';
import { SeriesFiltersBar, type SeriesFiltersValue } from '@/components/series/SeriesFiltersBar';
import { SeriesEmptyState } from '@/components/series/SeriesEmptyState';
import { SeriesFirstRunState } from '@/components/series/SeriesFirstRunState';

const DEFAULT_FILTERS: SeriesFiltersValue = {
  search: '',
  state: 'missing',
  sort: 'updated_desc',
  monitoredOnly: true,
  networks: new Set<string>(),
};

function isValidState(v: string | null): v is SeriesCacheStatus {
  return v === 'all' || v === 'imported' || v === 'missing';
}

function isValidSort(v: string | null): v is SeriesCacheSort {
  return v === 'updated_desc' || v === 'title_asc' || v === 'air_date_desc';
}

function readFiltersFromParams(p: URLSearchParams): SeriesFiltersValue {
  const stateRaw = p.get('state');
  const sortRaw = p.get('sort');
  const networksRaw = p.get('networks');
  const monitoredRaw = p.get('monitored');
  return {
    search: p.get('q') ?? '',
    state: isValidState(stateRaw) ? stateRaw : DEFAULT_FILTERS.state,
    sort: isValidSort(sortRaw) ? sortRaw : DEFAULT_FILTERS.sort,
    monitoredOnly: monitoredRaw === null
      ? DEFAULT_FILTERS.monitoredOnly
      : monitoredRaw === '1',
    networks: networksRaw ? new Set(networksRaw.split('|').filter(Boolean)) : new Set<string>(),
  };
}

function writeFiltersToParams(v: SeriesFiltersValue): URLSearchParams {
  const p = new URLSearchParams();
  if (v.search) p.set('q', v.search);
  if (v.state !== DEFAULT_FILTERS.state) p.set('state', v.state);
  if (v.sort !== DEFAULT_FILTERS.sort) p.set('sort', v.sort);
  if (v.monitoredOnly !== DEFAULT_FILTERS.monitoredOnly) {
    p.set('monitored', v.monitoredOnly ? '1' : '0');
  }
  if (v.networks.size > 0) p.set('networks', [...v.networks].sort().join('|'));
  return p;
}

function applyClientFilters(
  items: readonly SeriesCacheItem[],
  v: SeriesFiltersValue,
): readonly SeriesCacheItem[] {
  const q = v.search.trim().toLowerCase();
  return items.filter((it) => {
    if (q) {
      const hay = `${it.title} ${it.title_slug}`.toLowerCase();
      if (!hay.includes(q)) return false;
    }
    if (v.monitoredOnly && !it.monitored) return false;
    if (v.networks.size > 0) {
      const n = it.network ?? '';
      if (!v.networks.has(n)) return false;
    }
    return true;
  });
}

function uniqueNetworks(items: readonly SeriesCacheItem[]): readonly string[] {
  const set = new Set<string>();
  for (const it of items) {
    if (it.network && it.network.length > 0) set.add(it.network);
  }
  return [...set].sort((a, b) => a.localeCompare(b));
}

export function Series() {
  const { t } = useTranslation();
  useSetPageTitle(t('series.title'));

  const inst = useInstances();
  const { filter } = useInstanceFilter();
  const instances = inst.data?.instances ?? [];
  const current = filter ?? instances[0]?.name ?? null;

  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => readFiltersFromParams(params), [params]);

  const list = useSeriesCacheInfinite(
    current,
    { state: filters.state, sort: filters.sort, limit: 24 },
  );

  const rawItems = useMemo(
    () => flattenSeriesCachePages(list.data?.pages),
    [list.data?.pages],
  );
  const filtered = useMemo(() => applyClientFilters(rawItems, filters), [rawItems, filters]);
  const networks = useMemo(() => uniqueNetworks(rawItems), [rawItems]);
  const total = list.data?.pages?.[0]?.total ?? 0;

  // One-shot auto-fallback: bare-URL initial mount with state=missing
  // and total=0 silently flips to state=all and surfaces an inline
  // hint. Subsequent manual toggles back to 'missing' must NOT re-fire.
  const didAutoFallbackRef = useRef(false);
  const [fellBackToAll, dispatchFallback] = useReducer(
    (_state: boolean, action: 'mark' | 'reset') => action === 'mark',
    false,
  );

  useEffect(() => {
    if (didAutoFallbackRef.current) return;
    if (!list.isSuccess) return;
    if (filters.state !== 'missing') {
      didAutoFallbackRef.current = true;
      return;
    }
    if (total > 0) {
      didAutoFallbackRef.current = true;
      return;
    }
    didAutoFallbackRef.current = true;
    dispatchFallback('mark');
    const next = writeFiltersToParams({ ...filters, state: 'all' });
    setParams(next, { replace: true });
  }, [list.isSuccess, filters, total, setParams]);

  const onChange = useCallback((next: SeriesFiltersValue) => {
    setParams(writeFiltersToParams(next), { replace: true });
  }, [setParams]);

  const onClear = useCallback(() => {
    dispatchFallback('reset');
    setParams(new URLSearchParams(), { replace: true });
  }, [setParams]);

  const onLoadMore = useCallback(() => {
    if (list.hasNextPage && !list.isFetchingNextPage) {
      void list.fetchNextPage();
    }
  }, [list]);

  const onRefresh = useCallback(() => {
    void list.refetch();
  }, [list]);

  if (!inst.isPending && instances.length === 0) {
    return (
      <div>
        <SeriesFirstRunState />
      </div>
    );
  }

  const showEmptyServer = list.isSuccess && rawItems.length === 0;
  const showEmptyFiltered = !showEmptyServer && list.isSuccess && filtered.length === 0 && rawItems.length > 0;

  return (
    <div className="flex flex-col gap-4">
      <SeriesHeader
        shownCount={filtered.length}
        totalCount={total}
        isLoading={list.isFetching && !list.isFetchingNextPage}
        isError={list.isError}
        onRefresh={onRefresh}
      />

      <SeriesFiltersBar
        value={filters}
        availableNetworks={networks}
        defaults={DEFAULT_FILTERS}
        onChange={onChange}
        onClear={onClear}
      />

      {fellBackToAll && (
        <div
          className="text-[12.5px] text-tx-faint"
          data-testid="series-fallback-hint"
        >
          {t('series.filters.state.fallbackHint')}
        </div>
      )}

      {showEmptyServer && <SeriesEmptyState variant="server" />}
      {showEmptyFiltered && <SeriesEmptyState variant="filtered" onClearFilters={onClear} />}

      {!showEmptyServer && !showEmptyFiltered && (
        <SeriesGrid
          items={filtered}
          isLoading={list.isPending}
          isFetchingNextPage={list.isFetchingNextPage}
          hasNextPage={list.hasNextPage ?? false}
          onLoadMore={onLoadMore}
        />
      )}
    </div>
  );
}
