import { useMemo, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { Card, CardContent } from '@/components/ui/card';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { AlertTriangle } from 'lucide-react';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { useInstances } from '@/lib/instances';
import {
  useDecisionsList,
  useStuckSeasons,
  flattenDecisionList,
  applyDecisionsFilters,
  type DecisionsWindow,
  type DecisionsSort,
  type StuckSeason,
} from '@/lib/api/decisions';
import { categoryToBucket } from '@/lib/decisions/reasonCategory';
import { DecisionsHeader } from '@/components/decisions/DecisionsHeader';
import {
  DecisionsFiltersBar,
  type CategoryFilter,
} from '@/components/decisions/DecisionsFiltersBar';
import { StuckHero } from '@/components/decisions/StuckHero';
import { DecisionsEmptyState } from '@/components/decisions/DecisionsEmptyState';
import { DecisionsFirstRunState } from '@/components/decisions/DecisionsFirstRunState';
import { SkeletonRows } from '@/components/SkeletonRows';

const VALID_CATEGORIES: ReadonlySet<CategoryFilter> = new Set([
  'all', 'done', 'none', 'blocked', 'sonarr', 'ok',
]);
const VALID_WINDOWS: ReadonlySet<DecisionsWindow> = new Set([
  '24h', '7d', '30d', 'all',
]);
const VALID_SORTS: ReadonlySet<DecisionsSort> = new Set([
  'freshest', 'stuck-first',
]);

function parseCategory(raw: string | null): CategoryFilter {
  return raw && VALID_CATEGORIES.has(raw as CategoryFilter) ? (raw as CategoryFilter) : 'all';
}
function parseWindow(raw: string | null): DecisionsWindow {
  return raw && VALID_WINDOWS.has(raw as DecisionsWindow) ? (raw as DecisionsWindow) : '7d';
}
function parseSort(raw: string | null): DecisionsSort {
  return raw && VALID_SORTS.has(raw as DecisionsSort) ? (raw as DecisionsSort) : 'freshest';
}

export function Decisions() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const { filter: ctxInstance, setFilter: setCtxInstance } = useInstanceFilter();
  const instancesQ = useInstances();

  // URL state
  const search   = params.get('q') ?? '';
  const category = parseCategory(params.get('category'));
  const window   = parseWindow(params.get('window'));
  const sort     = parseSort(params.get('sort'));
  // Instance comes from the context (already URL-synced by the
  // AppShell switcher); we keep it readable here so the filter can
  // show it inside the bar.
  const instance = ctxInstance;

  const canReset =
    Boolean(search) ||
    category !== 'all' ||
    window !== '7d' ||
    sort !== 'freshest';

  const setParam = useCallback((k: string, v: string | null) => {
    const next = new URLSearchParams(params);
    if (!v) next.delete(k); else next.set(k, v);
    setParams(next, { replace: true });
  }, [params, setParams]);

  const onReset = useCallback(() => {
    setParams(new URLSearchParams(), { replace: true });
  }, [setParams]);

  // Queries
  const listQ  = useDecisionsList({ window });
  const stuckQ = useStuckSeasons({ window });

  const onOpenSeason = useCallback((s: StuckSeason) => {
    // 053b's drawer reads `?series=` / `?season=` from the URL. Until
    // the drawer mounts, the URL params are inert — they only become
    // meaningful when 053b's slot-swap lands.
    const next = new URLSearchParams(params);
    next.set('series', String(s.seriesId));
    next.set('season', String(s.seasonNumber));
    setParams(next, { replace: true });
  }, [params, setParams]);

  // Available instances for dropdown (excludes "all" placeholder)
  const availableInstances = useMemo(
    () => (instancesQ.data?.instances ?? []).map((i) => i.name).filter(Boolean) as string[],
    [instancesQ.data],
  );

  // Flatten + filter + sort
  const rows = useMemo(
    () => flattenDecisionList(listQ.data?.pages),
    [listQ.data],
  );
  const filtered = useMemo(
    () => applyDecisionsFilters(rows, { search, category, window, sort }),
    [rows, search, category, window, sort],
  );

  // Counts for the Результат dropdown — computed against the
  // unfiltered window so the user sees totals, not the post-filter
  // count (matches the design's 271/12/23/5/9/231 example).
  const counts: Record<CategoryFilter, number> = useMemo(() => {
    const c: Record<CategoryFilter, number> = {
      all: rows.length, done: 0, none: 0, blocked: 0, sonarr: 0, ok: 0,
    };
    for (const d of rows) c[categoryToBucket(d.category)] += 1;
    return c;
  }, [rows]);

  // Series count for the header — distinct series in the filtered set.
  const seriesCount = useMemo(() => {
    const s = new Set<number>();
    for (const d of filtered) s.add(d.series_id ?? -1);
    return s.size;
  }, [filtered]);

  // State branch resolution
  const isInitialLoading = listQ.isPending && rows.length === 0;
  const hasAnyDecisions = rows.length > 0;
  const filteredEmpty = filtered.length === 0;

  return (
    <div className="max-w-[940px] mx-auto p-6 flex flex-col gap-2">
      <header className="flex items-center gap-4 flex-wrap">
        <h1 className="text-[22px] font-semibold tracking-tight">
          {t('decisions.title')}
        </h1>
      </header>

      {listQ.isError && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('decisions.loadFailed')}</AlertTitle>
          <AlertDescription>
            {listQ.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => listQ.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {!isInitialLoading && !hasAnyDecisions && !listQ.isError && (
        <DecisionsFirstRunState />
      )}

      {!isInitialLoading && hasAnyDecisions && (
        <>
          <DecisionsHeader
            window={window}
            decisionsCount={filtered.length}
            seriesCount={seriesCount}
          />

          <StuckHero
            items={stuckQ.data}
            isLoading={stuckQ.isPending}
            onOpenSeason={onOpenSeason}
          />

          <DecisionsFiltersBar
            search={search}
            category={category}
            instance={instance}
            availableInstances={availableInstances}
            window={window}
            sort={sort}
            counts={counts}
            onSearchChange={(v) => setParam('q', v || null)}
            onCategoryChange={(v) => setParam('category', v === 'all' ? null : v)}
            onInstanceChange={(v) => setCtxInstance(v)}
            onWindowChange={(v) => setParam('window', v === '7d' ? null : v)}
            onSortChange={(v) => setParam('sort', v === 'freshest' ? null : v)}
            onReset={onReset}
            canReset={canReset}
          />

          {filteredEmpty ? (
            <DecisionsEmptyState onReset={onReset} />
          ) : (
            // Placeholder slot for 053b — keeps the test surface stable
            // and the DOM non-empty while the rest of the page renders.
            <Card>
              <CardContent className="p-4">
                <section data-testid="decisions-accordion-slot">
                  <SkeletonRows rows={3} cols={['sm', 'md', 'lg']} />
                </section>
              </CardContent>
            </Card>
          )}
        </>
      )}

      {isInitialLoading && (
        <Card>
          <CardContent className="p-4">
            <SkeletonRows rows={6} cols={['sm', 'md', 'lg', 'sm']} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
