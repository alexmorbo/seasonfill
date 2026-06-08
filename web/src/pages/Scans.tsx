import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { AlertTriangle } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/card';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { SkeletonRows } from '@/components/SkeletonRows';
import { Table, TableBody } from '@/components/ui/table';
import { NewScanModal } from '@/components/NewScanModal';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useScans, flattenScans, filterByTrigger, type ScanFilters } from '@/lib/scans';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { ScansHeader } from '@/components/scans/ScansHeader';
import { ScansFiltersBar, SCANS_DEFAULTS, type ScansFiltersValue } from '@/components/scans/ScansFiltersBar';
import { ScansTable } from '@/components/scans/ScansTable';
import { ScansEmptyState } from '@/components/scans/ScansEmptyState';
import { ScansFirstRunState } from '@/components/scans/ScansFirstRunState';

function windowToDates(window: string): { from?: string; to?: string } {
  if (!window || window === 'all') return {};
  const now = Date.now();
  const ms =
    window === '24h' ? 24 * 3600 * 1000 :
    window === '7d'  ? 7 * 24 * 3600 * 1000 :
    window === '30d' ? 30 * 24 * 3600 * 1000 : 0;
  if (!ms) return {};
  return { from: new Date(now - ms).toISOString(), to: new Date(now).toISOString() };
}

function readFilters(params: URLSearchParams): ScansFiltersValue {
  return {
    status:  params.get('status')  ?? SCANS_DEFAULTS.status,
    trigger: params.get('trigger') ?? SCANS_DEFAULTS.trigger,
    window:  params.get('window')  ?? SCANS_DEFAULTS.window,
  };
}

export function Scans() {
  const { t } = useTranslation();
  useSetPageTitle(t('scans.title'));
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();
  const [scanModalOpen, setScanModalOpen] = useState(false);

  const filters = useMemo<ScansFiltersValue>(() => readFilters(params), [params]);
  const isFiltered =
    filters.status !== SCANS_DEFAULTS.status ||
    filters.trigger !== SCANS_DEFAULTS.trigger ||
    filters.window !== SCANS_DEFAULTS.window;

  const queryFilters: ScanFilters = useMemo(() => {
    const w = windowToDates(filters.window);
    return {
      ...(filters.status !== 'all' && { status: filters.status }),
      ...(w.from && { from: w.from }),
      ...(w.to && { to: w.to }),
    };
  }, [filters]);

  const q = useScans(queryFilters);
  const allRows = useMemo(() => flattenScans(q.data?.pages), [q.data]);
  const rows = useMemo(
    () => filterByTrigger(allRows, filters.trigger === 'all' ? undefined : filters.trigger),
    [allRows, filters.trigger],
  );

  const setFilters = (next: ScansFiltersValue) => {
    const sp = new URLSearchParams(params);
    if (next.status === SCANS_DEFAULTS.status) sp.delete('status'); else sp.set('status', next.status);
    if (next.trigger === SCANS_DEFAULTS.trigger) sp.delete('trigger'); else sp.set('trigger', next.trigger);
    if (next.window === SCANS_DEFAULTS.window) sp.delete('window'); else sp.set('window', next.window);
    setParams(sp, { replace: true });
  };
  const resetFilters = () => setFilters(SCANS_DEFAULTS);

  const showFirstRun  = !q.isPending && !q.isError && !isFiltered && allRows.length === 0;
  const showFiltered  = !q.isPending && !q.isError && isFiltered && rows.length === 0;
  const showTable     = !q.isError && (q.isPending || rows.length > 0 || (allRows.length > 0 && !isFiltered));

  return (
    <div className="flex flex-col gap-4">
      <ScansHeader count={rows.length} instance={instance} />
      <div className="flex items-center gap-2">
        <ScansFiltersBar value={filters} onChange={setFilters} />
      </div>

      <Card>
        <CardContent className="p-0">
          {q.isError && (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>{t('scans.loadFailed')}</AlertTitle>
              <AlertDescription>
                {q.error.message}{' '}
                <Button variant="link" size="sm" onClick={() => q.refetch()}>
                  {t('common.retry')}
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {q.isPending && (
            <Table>
              <TableBody>
                <SkeletonRows rows={6} cols={['xs', 'sm', 'sm', 'md', 'sm', 'sm', 'sm', 'sm', 'xs']} />
              </TableBody>
            </Table>
          )}
          {showFirstRun  && <ScansFirstRunState onTriggerScan={() => setScanModalOpen(true)} />}
          {showFiltered  && <ScansEmptyState onReset={resetFilters} />}
          {showTable && !q.isPending && rows.length > 0 && <ScansTable rows={rows} />}
        </CardContent>
      </Card>

      {q.hasNextPage && rows.length > 0 && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            onClick={() => q.fetchNextPage()}
            disabled={q.isFetchingNextPage}
          >
            {q.isFetchingNextPage ? t('common.loading') : t('common.loadMore')}
          </Button>
        </div>
      )}

      <NewScanModal open={scanModalOpen} onOpenChange={setScanModalOpen} />
    </div>
  );
}
