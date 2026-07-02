import { useCallback, useEffect, useMemo, useReducer, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useInstances } from '@/lib/instances';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useLanguage } from '@/hooks/useLanguage';
import {
  useSeriesCacheInfinite,
  useSeriesCacheNetworks,
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
import { SeriesScanRunningState } from '@/components/series/SeriesScanRunningState';
import { SeriesFirstScanState } from '@/components/series/SeriesFirstScanState';
import { SeriesAllHealthyState } from '@/components/series/SeriesAllHealthyState';
import { useInstanceLatestScan } from '@/lib/scans';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { AlertTriangle } from 'lucide-react';

// Story 495 / N-1e (B-15): empty-state branches surfaced on `/series`.
// `firstRun`     — no Sonarr instances configured.
// `scanRunning`  — instance exists AND latest scan is in flight.
// `firstScan`    — instance exists AND no scan has ever run (cache empty).
// `allHealthy`   — instance exists AND latest scan completed with 0 finds.
// `filtered`     — server returned >0 rows but client filters eliminated them.
// `null`         — render the regular grid.
export type EmptyBranch =
  | 'firstRun'
  | 'scanRunning'
  | 'firstScan'
  | 'allHealthy'
  | 'filtered'
  | null;

export interface DecideEmptyBranchArgs {
  readonly instancesPending: boolean;
  readonly instanceCount: number;
  readonly listSuccess: boolean;
  readonly rawCount: number;
  readonly filteredCount: number;
  readonly total: number;
  readonly latestScanStatus: string | undefined;
  // `true` when `useInstanceLatestScan` has resolved (data === null OR
  // data is a Scan); `false` while the query is still pending.
  readonly latestScanResolved: boolean;
}

export function decideEmptyBranch(args: DecideEmptyBranchArgs): EmptyBranch {
  if (!args.instancesPending && args.instanceCount === 0) return 'firstRun';
  if (!args.listSuccess) return null;
  if (args.latestScanStatus === 'running') return 'scanRunning';
  if (args.rawCount > 0 && args.filteredCount === 0) return 'filtered';
  // `latestScanResolved && latestScanStatus === undefined` ⇒ no run row exists
  // yet for this instance, AND the cache is empty ⇒ first-scan branch.
  if (args.latestScanResolved && args.latestScanStatus === undefined && args.rawCount === 0) {
    return 'firstScan';
  }
  if (args.latestScanStatus === 'completed' && args.rawCount === 0 && args.total === 0) {
    return 'allHealthy';
  }
  return null;
}

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

// applyClientFilters is now a pass-through. Kept as a hook point in
// case a future story adds a client-side overlay (e.g. last-watched
// flagging). Story 121a §A: monitoredOnly + networks moved to the
// repo edge to make them work past page 1.
function applyClientFilters(
  items: readonly SeriesCacheItem[],
  _v: SeriesFiltersValue,
): readonly SeriesCacheItem[] {
  return items;
}

export function Series() {
  const { t } = useTranslation();
  useSetPageTitle(t('series.title'));

  const inst = useInstances();
  const { filter } = useInstanceFilter();
  const instances = inst.data?.instances ?? [];
  const current = filter ?? instances[0]?.name ?? null;
  const lang = useLanguage().current;

  const [params, setParams] = useSearchParams();
  const filters = useMemo(() => readFiltersFromParams(params), [params]);

  const list = useSeriesCacheInfinite(
    current,
    {
      state: filters.state,
      sort: filters.sort,
      limit: 24,
      search: filters.search,
      lang,
      // Story 121a §A. Conditional-spread under exactOptionalPropertyTypes:
      // toggle on → send monitored=1; toggle off → omit (any).
      ...(filters.monitoredOnly ? { monitoredOnly: true as const } : {}),
      ...(filters.networks.size > 0 ? { networks: [...filters.networks] } : {}),
    },
  );

  const rawItems = useMemo(
    () => flattenSeriesCachePages(list.data?.pages),
    [list.data?.pages],
  );
  const filtered = useMemo(() => applyClientFilters(rawItems, filters), [rawItems, filters]);
  // Story 121a §A: facet panel reads the full distinct set from the
  // sibling endpoint instead of deriving it from loaded pages.
  const networksQuery = useSeriesCacheNetworks(current);
  const networks = networksQuery.data ?? [];
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

  // Story 495 / N-1e (B-15): poll latest scan for the current instance
  // so we can branch between "scan in progress" / "first scan" /
  // "all healthy". `useInstanceLatestScan` is `enabled: Boolean(instance)`
  // so it short-circuits when no instance is selected.
  const latestScan = useInstanceLatestScan(current ?? undefined);

  if (!inst.isPending && instances.length === 0) {
    return (
      <div>
        <SeriesFirstRunState />
      </div>
    );
  }

  const branch = decideEmptyBranch({
    instancesPending: inst.isPending,
    instanceCount: instances.length,
    listSuccess: list.isSuccess,
    rawCount: rawItems.length,
    filteredCount: filtered.length,
    total,
    latestScanStatus: latestScan.data?.status,
    latestScanResolved: !latestScan.isPending,
  });

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

      {/* Story 121b §I: surface list-fetch failures inline. Mirrors
          Decisions.tsx:171, Scans.tsx:90, Grabs.tsx:167. */}
      {list.isError && (
        <Alert
          variant="destructive"
          data-testid="series-list-error"
        >
          <AlertTriangle className="h-4 w-4" />
          <AlertTitle>{t('series.errors.listFailedTitle')}</AlertTitle>
          <AlertDescription>
            {list.error instanceof Error
              ? list.error.message
              : t('series.errors.listFailedDescription')}
          </AlertDescription>
        </Alert>
      )}

      {/* Story 495 / N-1e (B-15): per-branch empty states. The grid
          renders only when no branch is active and the list is healthy. */}
      {branch === 'scanRunning' && latestScan.data?.id && (
        <SeriesScanRunningState scanRunId={latestScan.data.id} />
      )}
      {branch === 'firstScan' && current && (
        <SeriesFirstScanState instance={current} />
      )}
      {branch === 'allHealthy' && <SeriesAllHealthyState />}
      {branch === 'filtered' && (
        <SeriesEmptyState variant="filtered" onClearFilters={onClear} />
      )}

      {branch === null && !list.isError && (
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
