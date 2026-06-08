import { useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useGrabs, flattenGrabs, type Grab } from '@/lib/grabs';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { GrabsFiltersBar, type GrabFilter } from '@/components/grabs/GrabsFiltersBar';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { GrabRow } from '@/components/grabs/GrabRow';
import { GrabsEmptyState } from '@/components/grabs/GrabsEmptyState';
import { GrabDrawer } from '@/components/GrabDrawer'; // existing — 051b replaces
import { SkeletonRows } from '@/components/SkeletonRows';
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { AlertTriangle } from 'lucide-react';

const FAIL_STATUSES = new Set(['import_failed', 'grab_failed', 'expired']);

function filterRows(rows: readonly Grab[], filter: GrabFilter, search: string): Grab[] {
  const needle = search.trim().toLowerCase();
  return rows.filter((g) => {
    if (filter === 'active'  && g.status !== 'grabbed') return false;
    if (filter === 'history' && g.status !== 'imported') return false;
    if (filter === 'fails'   && !FAIL_STATUSES.has(g.status ?? '')) return false;
    if (!needle) return true;
    return (
      (g.series_title  ?? '').toLowerCase().includes(needle) ||
      (g.release_title ?? '').toLowerCase().includes(needle)
    );
  });
}

function computeCounts(rows: readonly Grab[]) {
  let active = 0, history = 0, fails = 0;
  for (const g of rows) {
    if (g.status === 'grabbed')   active++;
    if (g.status === 'imported')  history++;
    if (FAIL_STATUSES.has(g.status ?? '')) fails++;
  }
  return { all: rows.length, active, history, fails };
}

export function Grabs() {
  const { t } = useTranslation();
  useSetPageTitle(t('grabs.title'));
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { filter: instance } = useInstanceFilter();

  const filter = (params.get('filter') as GrabFilter | null) ?? 'all';
  const search = params.get('q') ?? '';
  const openId = params.get('open');
  const threadId = params.get('thread');
  const seriesParam = params.get('series');
  const seriesIDFilter = seriesParam !== null && seriesParam !== ''
    ? Number.parseInt(seriesParam, 10)
    : Number.NaN;
  const seriesID = Number.isFinite(seriesIDFilter) ? seriesIDFilter : undefined;

  // Debounced search — typing into the input updates a local string that
  // pushes into URL state after 250 ms idle.
  const [searchLocal, setSearchLocal] = useState(() => search);
  const debounceRef = useRef<number | undefined>(undefined);
  const pushSearch = useCallback((v: string) => {
    window.clearTimeout(debounceRef.current);
    debounceRef.current = window.setTimeout(() => {
      const next = new URLSearchParams(params);
      if (!v) next.delete('q'); else next.set('q', v);
      setParams(next, { replace: true });
    }, 250);
  }, [params, setParams]);
  const onSearchChange = (v: string) => { setSearchLocal(v); pushSearch(v); };

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (!v) next.delete(k); else next.set(k, v);
    setParams(next, { replace: true });
  };

  const refetchMs = filter === 'active' ? 30_000 : 60_000;
  const query = useGrabs(
    seriesID !== undefined ? { series_id: seriesID } : {},
    { refetchMs },
  );

  const all = useMemo(() => flattenGrabs(query.data?.pages), [query.data]);
  const counts = useMemo(() => computeCounts(all), [all]);
  const rows = useMemo(() => filterRows(all, filter, searchLocal), [all, filter, searchLocal]);

  // Build a per-grab "re-grab index" map by walking the replay_of_id chain.
  // For each row R with R.replay_of_id, climb the chain in `all` and count
  // the depth. Roots have index null (no `↻ #N` tag); first re-grab is #1.
  const reGrabIndex = useMemo(() => {
    const byId = new Map<string, Grab>();
    for (const g of all) if (g.id) byId.set(g.id, g);
    const idxOf = new Map<string, number>();
    const visit = (g: Grab): number => {
      if (!g.id) return 0;
      const cached = idxOf.get(g.id);
      if (cached !== undefined) return cached;
      const parentId = g.replay_of_id;
      if (!parentId) { idxOf.set(g.id, 0); return 0; }
      const parent = byId.get(parentId);
      const depth = parent ? visit(parent) + 1 : 1;
      idxOf.set(g.id, depth);
      return depth;
    };
    for (const g of all) visit(g);
    return idxOf;
  }, [all]);

  const openDrawer = (id: string) => setParam('open', id);
  const toggleThread = (id: string) => setParam('thread', threadId === id ? '' : id);

  const showEmpty = !query.isPending && rows.length === 0;
  const emptyVariant: 'top' | 'fails' | 'search' =
    searchLocal.trim() ? 'search'
    : filter === 'fails' ? 'fails'
    : 'top';

  return (
    <div className="flex flex-col gap-4 relative">
      <GrabsFiltersBar
        filter={filter}
        onFilterChange={(f) => setParam('filter', f === 'all' ? '' : f)}
        counts={counts}
        search={searchLocal}
        onSearchChange={onSearchChange}
        instance={instance ?? null}
      />
      {query.isError ? (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" />
          <AlertTitle>{t('grabs.loadFailed')}</AlertTitle>
          <AlertDescription>
            {query.error.message}{' '}
            <Button variant="link" size="sm" onClick={() => query.refetch()}>
              {t('common.retry')}
            </Button>
          </AlertDescription>
        </Alert>
      ) : query.isPending ? (
        <SkeletonRows rows={6} cols={['lg', 'lg', 'lg', 'lg']} />
      ) : showEmpty ? (
        <GrabsEmptyState
          variant={emptyVariant}
          onScan={() => navigate('/scans?action=new')}
          onQueue={instance ? () => navigate(`/instances/${instance}/queue`) : undefined}
          queueCount={counts.all}
          onClearSearch={() => onSearchChange('')}
        />
      ) : (
        <div className="flex flex-col gap-2">
          {rows.map((g) => (
            <GrabRow
              key={g.id}
              grab={g}
              selected={openId === g.id}
              threadOpen={threadId === g.id}
              reGrabIndex={reGrabIndex.get(g.id ?? '') ?? null}
              instance={instance ?? null}
              localAll={all}
              onOpenDrawer={openDrawer}
              onToggleThread={toggleThread}
            />
          ))}
          {query.hasNextPage && (
            <div className="flex justify-center pt-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => query.fetchNextPage()}
                disabled={query.isFetchingNextPage}
              >
                {query.isFetchingNextPage ? t('common.loading') : t('common.loadMore')}
              </Button>
            </div>
          )}
        </div>
      )}
      {/* Drawer kept as today; 051b replaces with new GrabDrawer */}
      <GrabDrawer
        id={openId ?? null}
        open={Boolean(openId)}
        onOpenChange={(o) => (o ? null : setParam('open', ''))}
        rows={all}
      />
    </div>
  );
}
