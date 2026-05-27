import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Card, CardContent } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Check, AlertTriangle } from 'lucide-react';
import { StatusBadge } from '@/components/StatusBadge';
import { SkeletonRows } from '@/components/SkeletonRows';
import { EmptyState } from '@/components/EmptyState';
import { useScans, flattenScans, type Scan, type ScanFilters } from '@/lib/scans';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime, durationMs } from '@/lib/format';
import {
  applySort,
  cmpDate,
  cmpNumber,
  cmpString,
  useTableSort,
  type Comparator,
} from '@/lib/use-sort';
import { SortableHeader } from '@/components/SortableHeader';

const STATUSES = ['running', 'completed', 'failed', 'aborted'] as const;

const SCAN_COMPARATORS: Readonly<Record<string, Comparator<Scan>>> = {
  started_at: cmpDate<Scan>((s) => s.started_at),
  instance: cmpString<Scan>((s) => s.instance),
  status: cmpString<Scan>((s) => s.status),
  grabs: cmpNumber<Scan>((s) => s.grabs_performed),
};

export function Scans() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();

  const status = params.get('status') ?? '';
  const queryFilters: ScanFilters = useMemo(
    () => ({ ...(status && { status }) }),
    [status],
  );

  const q = useScans(queryFilters);
  const unsortedRows = useMemo(() => flattenScans(q.data?.pages), [q.data]);
  const { sortKey, dir, toggle } = useTableSort();
  const rows = useMemo(
    () => applySort(unsortedRows, SCAN_COMPARATORS, sortKey, dir),
    [unsortedRows, sortKey, dir],
  );

  const updateParam = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (!value) next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };
  const clear = () => setParams(new URLSearchParams(), { replace: true });

  const handleRowKey = (e: React.KeyboardEvent, id: string | undefined) => {
    if ((e.key === 'Enter' || e.key === ' ') && id) {
      e.preventDefault();
      navigate(`/scans/${id}`);
    }
  };

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-4">
      <header className="flex items-center gap-4 flex-wrap">
        <h1 className="text-[22px] font-semibold tracking-tight">{t('scans.title')}</h1>
        <span className="font-mono text-[12px] text-faint">
          {t('scans.loadedCount', { count: rows.length })}{instance ? ` · ${t('scans.instanceLabel', { name: instance })}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">{t('decisions.filter')}</span>
        <Select
          value={status || 'all'}
          onValueChange={(v) => updateParam('status', v === 'all' ? '' : v)}
        >
          <SelectTrigger className="h-8 w-[140px] text-[12.5px]" aria-label={t('scans.anyStatus')}>
            <SelectValue placeholder={t('scans.anyStatus')} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('scans.anyStatus')}</SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select defaultValue="24h">
          <SelectTrigger className="h-8 w-[120px] text-[12.5px]" aria-label={t('decisions.timeRangeAria')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="24h">{t('decisions.range.h24')}</SelectItem>
            <SelectItem value="7d">{t('decisions.range.d7')}</SelectItem>
            <SelectItem value="30d">{t('decisions.range.d30')}</SelectItem>
          </SelectContent>
        </Select>
        <div className="flex-1" />
        <Button variant="ghost" size="sm" onClick={clear} disabled={!status}>
          {t('decisions.clear')}
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {q.isError ? (
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
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8"></TableHead>
                  <TableHead>{t('scans.columns.id')}</TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('scans.columns.instance')}
                      sortKey="instance"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('scans.columns.trigger')}</TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('scans.columns.started')}
                      sortKey="started_at"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('scans.columns.duration')}</TableHead>
                  <TableHead>{t('scans.columns.series')}</TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('scans.columns.grabs')}
                      sortKey="grabs"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('scans.columns.status')}
                      sortKey="status"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {q.isPending && (
                  <SkeletonRows
                    rows={6}
                    cols={['xs', 'md', 'md', 'sm', 'md', 'sm', 'sm', 'sm', 'sm']}
                  />
                )}
                {!q.isPending && rows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={9}>
                      <EmptyState
                        title={t('scans.empty.matchTitle')}
                        body={t('scans.empty.matchBody')}
                        {...(status
                          ? {
                              action: (
                                <Button variant="outline" size="sm" onClick={clear}>
                                  {t('decisions.clearFilters')}
                                </Button>
                              ),
                            }
                          : {})}
                      />
                    </TableCell>
                  </TableRow>
                )}
                {rows.map((s) => (
                  <TableRow
                    key={s.id}
                    onClick={() => s.id && navigate(`/scans/${s.id}`)}
                    onKeyDown={(e) => handleRowKey(e, s.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={t('dashboard.recent.openScan', { id: (s.id ?? '').slice(0, 8) })}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <TableCell>
                      {s.status === 'failed' ? (
                        <AlertTriangle className="w-3.5 h-3.5 text-status-danger" />
                      ) : (
                        <Check className="w-3.5 h-3.5 text-status-success" />
                      )}
                    </TableCell>
                    <TableCell>
                      <span className="font-mono text-[12px] text-foreground-2">
                        {(s.id ?? '').slice(0, 8)}
                      </span>
                    </TableCell>
                    <TableCell className="font-mono">{s.instance}</TableCell>
                    <TableCell>
                      <StatusBadge value={s.trigger} />
                    </TableCell>
                    <TableCell className="text-muted">{relativeTime(s.started_at)}</TableCell>
                    <TableCell className="font-mono">
                      {durationMs(s.started_at, s.finished_at)}
                    </TableCell>
                    <TableCell className="font-mono">{s.series_scanned ?? 0}</TableCell>
                    <TableCell className="font-mono">{s.grabs_performed ?? 0}</TableCell>
                    <TableCell>
                      <StatusBadge value={s.status} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {q.hasNextPage && (
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
    </div>
  );
}
