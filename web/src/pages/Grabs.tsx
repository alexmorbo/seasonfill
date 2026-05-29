import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
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
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { AlertTriangle } from 'lucide-react';
import { StatusBadge } from '@/components/StatusBadge';
import { SkeletonRows } from '@/components/SkeletonRows';
import { EmptyState } from '@/components/EmptyState';
import { GrabDrawer } from '@/components/GrabDrawer';
import { useGrabs, flattenGrabs, type Grab, type GrabFilters } from '@/lib/grabs';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime } from '@/lib/format';
import {
  applySort,
  cmpDate,
  cmpString,
  useTableSort,
  type Comparator,
} from '@/lib/use-sort';
import { SortableHeader } from '@/components/SortableHeader';

const GRAB_COMPARATORS: Readonly<Record<string, Comparator<Grab>>> = {
  created_at: cmpDate<Grab>((g) => g.updated_at ?? g.created_at),
  instance: cmpString<Grab>((g) => g.instance),
  series_title: cmpString<Grab>((g) => g.series_title),
  status: cmpString<Grab>((g) => g.status),
};

const STATUSES = ['grabbed', 'imported', 'import_failed', 'grab_failed', 'expired'] as const;

export function Grabs() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();
  const status = params.get('status') ?? '';
  const q = params.get('q') ?? '';
  const drawer = params.get('drawer');

  const queryFilters: GrabFilters = useMemo(() => ({ ...(status && { status }) }), [status]);
  const query = useGrabs(queryFilters);
  const allRows = useMemo(() => flattenGrabs(query.data?.pages), [query.data]);
  const { sortKey, dir, toggle } = useTableSort();
  const filtered = useMemo(() => {
    if (!q) return allRows;
    const needle = q.toLowerCase();
    return allRows.filter(
      (g) =>
        (g.series_title ?? '').toLowerCase().includes(needle) ||
        (g.release_title ?? '').toLowerCase().includes(needle),
    );
  }, [allRows, q]);
  const rows = useMemo(
    () => applySort(filtered, GRAB_COMPARATORS, sortKey, dir),
    [filtered, sortKey, dir],
  );

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (!v) next.delete(k);
    else next.set(k, v);
    setParams(next, { replace: true });
  };
  const clear = () => setParams(new URLSearchParams(), { replace: true });
  const openDrawer = (id: string | undefined) => id && setParam('drawer', id);
  const closeDrawer = () => setParam('drawer', '');
  const onKey = (e: React.KeyboardEvent, id: string | undefined) => {
    if ((e.key === 'Enter' || e.key === ' ') && id) {
      e.preventDefault();
      openDrawer(id);
    }
  };

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-4">
      <header className="flex items-center gap-4 flex-wrap">
        <h1 className="text-[22px] font-semibold tracking-tight">{t('grabs.title')}</h1>
        <span className="font-mono text-[12px] text-faint">
          {t('grabs.loadedCount', { count: rows.length })}{instance ? ` · ${instance}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">{t('grabs.filter')}</span>
        <Select
          value={status || 'all'}
          onValueChange={(v) => setParam('status', v === 'all' ? '' : v)}
        >
          <SelectTrigger className="h-8 w-[160px] text-[12.5px]" aria-label={t('grabs.anyStatus')}>
            <SelectValue placeholder={t('grabs.anyStatus')} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t('grabs.anyStatus')}</SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {t(`statuses.${s}`, { defaultValue: s })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          placeholder={t('grabs.searchPlaceholder')}
          value={q}
          onChange={(e) => setParam('q', e.target.value)}
          className="h-8 w-[260px] text-[12.5px]"
        />
        <Select defaultValue="24h">
          <SelectTrigger className="h-8 w-[120px] text-[12.5px]" aria-label={t('grabs.timeRangeAria')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="24h">{t('grabs.range.h24')}</SelectItem>
            <SelectItem value="7d">{t('grabs.range.d7')}</SelectItem>
            <SelectItem value="30d">{t('grabs.range.d30')}</SelectItem>
          </SelectContent>
        </Select>
        <div className="flex-1" />
        <Button variant="ghost" size="sm" onClick={clear} disabled={!status && !q}>
          {t('grabs.clear')}
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {query.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>{t('grabs.loadFailed')}</AlertTitle>
              <AlertDescription>
                {query.error.message}{' '}
                <Button variant="link" size="sm" onClick={() => query.refetch()}>
                  {t('common.retry')}
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>
                    <SortableHeader
                      label={t('grabs.columns.time')}
                      sortKey="created_at"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('grabs.columns.instance')}
                      sortKey="instance"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('grabs.columns.series')}
                      sortKey="series_title"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('grabs.columns.release')}</TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('grabs.columns.status')}
                      sortKey="status"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('grabs.columns.indexer')}</TableHead>
                  <TableHead>{t('grabs.columns.attempts')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.isPending && (
                  <SkeletonRows
                    rows={6}
                    cols={['sm', 'md', 'lg', '2xl', 'sm', 'md', 'sm']}
                  />
                )}
                {!query.isPending && rows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={7}>
                      <EmptyState
                        title={t('grabs.empty.matchTitle')}
                        body={t('grabs.empty.matchBody')}
                        {...(status || q
                          ? {
                              action: (
                                <Button variant="outline" size="sm" onClick={clear}>
                                  {t('grabs.clearFilters')}
                                </Button>
                              ),
                            }
                          : {})}
                      />
                    </TableCell>
                  </TableRow>
                )}
                {rows.map((g) => (
                  <TableRow
                    key={g.id}
                    onClick={() => openDrawer(g.id)}
                    onKeyDown={(e) => onKey(e, g.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={t('grabs.openGrabAria', { id: g.id ?? '' })}
                    className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <TableCell className="text-muted">
                      {relativeTime(g.updated_at ?? g.created_at)}
                    </TableCell>
                    <TableCell className="font-mono">{g.instance ?? '—'}</TableCell>
                    <TableCell className="font-medium">{g.series_title ?? '—'}</TableCell>
                    <TableCell className="font-mono text-[12px] max-w-md truncate">
                      {g.release_title ?? '—'}
                    </TableCell>
                    <TableCell>
                      <StatusBadge value={g.status} />
                    </TableCell>
                    <TableCell className="font-mono text-muted">{g.indexer_name ?? '—'}</TableCell>
                    <TableCell className="font-mono">{g.attempts ?? 0}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {query.hasNextPage && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            onClick={() => query.fetchNextPage()}
            disabled={query.isFetchingNextPage}
          >
            {query.isFetchingNextPage ? t('common.loading') : t('common.loadMore')}
          </Button>
        </div>
      )}

      <GrabDrawer
        id={drawer ?? null}
        open={Boolean(drawer)}
        onOpenChange={(o) => (o ? null : closeDrawer())}
        rows={allRows}
      />
    </div>
  );
}
