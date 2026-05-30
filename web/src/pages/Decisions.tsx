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
import { CategoryChip } from '@/components/CategoryChip';
import { SkeletonRows } from '@/components/SkeletonRows';
import { EmptyState } from '@/components/EmptyState';
import { OutcomeChips } from '@/components/OutcomeChips';
import { OUTCOMES, type Outcome } from '@/lib/outcomes';
import { DecisionDrawer } from '@/components/DecisionDrawer';
import {
  useDecisions,
  flattenDecisions,
  type Decision,
  type DecisionFilters,
} from '@/lib/decisions';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime } from '@/lib/format';
import {
  applySort,
  cmpDate,
  cmpNumber,
  cmpString,
  useTableSort,
  type Comparator,
} from '@/lib/use-sort';
import { SortableHeader } from '@/components/SortableHeader';
import { useInfiniteScroll } from '@/lib/use-infinite-scroll';

const DECISION_COMPARATORS: Readonly<Record<string, Comparator<Decision>>> = {
  created_at: cmpDate<Decision>((d) => d.created_at),
  instance: cmpString<Decision>((d) => d.instance),
  series_title: cmpString<Decision>((d) => d.series_title),
  season: cmpNumber<Decision>((d) => d.season_number),
  category: cmpString<Decision>((d) => d.category),
};

export function Decisions() {
  const { t } = useTranslation();
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();
  const outcomeParam = params.get('outcome') ?? '';
  const q = params.get('q') ?? '';
  const drawer = params.get('drawer');

  const selected = useMemo<Set<Outcome>>(() => {
    const set = new Set<Outcome>();
    for (const t of outcomeParam.split(',').filter(Boolean)) {
      if ((OUTCOMES as readonly string[]).includes(t)) set.add(t as Outcome);
    }
    return set;
  }, [outcomeParam]);

  const first = selected.size > 0 ? [...selected][0] : undefined;
  const queryFilters: DecisionFilters = useMemo(
    () => ({ ...(first !== undefined && { decision: first }) }),
    [first],
  );
  const query = useDecisions(queryFilters);
  const allRows = useMemo(() => flattenDecisions(query.data?.pages), [query.data]);
  const { sortKey, dir, toggle } = useTableSort();
  const filtered = useMemo(
    () =>
      allRows.filter((d) => {
        if (selected.size > 0 && d.decision && !selected.has(d.decision as Outcome)) return false;
        if (q && !(d.series_title ?? '').toLowerCase().includes(q.toLowerCase())) return false;
        return true;
      }),
    [allRows, selected, q],
  );
  const rows = useMemo(
    () => applySort(filtered, DECISION_COMPARATORS, sortKey, dir),
    [filtered, sortKey, dir],
  );

  const { sentinelRef } = useInfiniteScroll({
    hasNextPage: query.hasNextPage,
    isFetchingNextPage: query.isFetchingNextPage,
    fetchNextPage: query.fetchNextPage,
  });

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (!v) next.delete(k);
    else next.set(k, v);
    setParams(next, { replace: true });
  };
  const toggleOutcome = (o: Outcome) => {
    const next = new Set(selected);
    if (next.has(o)) next.delete(o);
    else next.add(o);
    setParam('outcome', [...next].join(','));
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
        <h1 className="text-[22px] font-semibold tracking-tight">{t('decisions.title')}</h1>
        <span className="font-mono text-[12px] text-faint">
          {t('decisions.loadedCount', { count: rows.length })}{instance ? ` · ${instance}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">{t('decisions.filter')}</span>
        <Input
          placeholder={t('decisions.searchPlaceholder')}
          value={q}
          onChange={(e) => setParam('q', e.target.value)}
          className="h-8 w-[220px] text-[12.5px]"
        />
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
        <Button variant="ghost" size="sm" onClick={clear} disabled={selected.size === 0 && !q}>
          {t('decisions.clear')}
        </Button>
      </div>

      <OutcomeChips selected={selected} onToggle={toggleOutcome} />
      {selected.size > 1 && (
        <p className="text-[11px] text-muted -mt-1 font-mono">
          {t('decisions.multiOutcomeNote')}
        </p>
      )}

      <Card>
        <CardContent className="p-0">
          {query.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>{t('decisions.loadFailed')}</AlertTitle>
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
                      label={t('decisions.columns.time')}
                      sortKey="created_at"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('decisions.columns.instance')}
                      sortKey="instance"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('decisions.columns.series')}
                      sortKey="series_title"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('decisions.columns.season')}
                      sortKey="season"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('decisions.columns.outcome')}</TableHead>
                  <TableHead>
                    <SortableHeader
                      label={t('decisions.columns.category')}
                      sortKey="category"
                      currentKey={sortKey}
                      currentDir={dir}
                      onToggle={toggle}
                    />
                  </TableHead>
                  <TableHead>{t('decisions.columns.reason')}</TableHead>
                  <TableHead>{t('decisions.columns.candidates')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.isPending && rows.length === 0 && (
                  <SkeletonRows
                    rows={8}
                    cols={['sm', 'md', 'lg', 'sm', 'md', 'md', 'xl', 'sm']}
                  />
                )}
                {!query.isPending && rows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={8}>
                      <EmptyState
                        title={t('decisions.empty.matchTitle')}
                        body={t('decisions.empty.matchBody')}
                        {...(selected.size > 0 || q
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
                {rows.map((d) => (
                  <TableRow
                    key={d.id}
                    onClick={() => openDrawer(d.id)}
                    onKeyDown={(e) => onKey(e, d.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={t('decisions.openDecisionAria', { id: d.id ?? '' })}
                    className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <TableCell className="text-muted">{relativeTime(d.created_at)}</TableCell>
                    <TableCell className="font-mono">{d.instance ?? '—'}</TableCell>
                    <TableCell className="font-medium">{d.series_title ?? '—'}</TableCell>
                    <TableCell className="font-mono">
                      {d.season_number !== undefined ? `S${String(d.season_number).padStart(2, '0')}` : '—'}
                    </TableCell>
                    <TableCell>
                      <StatusBadge value={d.decision} mode="outcome" />
                    </TableCell>
                    <TableCell>
                      <CategoryChip value={d.category} variant="compact" />
                    </TableCell>
                    <TableCell className="text-muted text-[12px] truncate max-w-md">
                      {d.reason ? t(`reasons.${d.reason}`, { defaultValue: d.reason }) : '—'}
                    </TableCell>
                    <TableCell className="font-mono">{d.candidates_count ?? 0}</TableCell>
                  </TableRow>
                ))}
                {query.isFetchingNextPage && rows.length > 0 && (
                  <SkeletonRows
                    rows={3}
                    cols={['sm', 'md', 'lg', 'sm', 'md', 'md', 'xl', 'sm']}
                  />
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {query.hasNextPage && (
        <div ref={sentinelRef} aria-hidden="true" className="h-1" />
      )}

      <DecisionDrawer
        id={drawer ?? null}
        open={Boolean(drawer)}
        onOpenChange={(o) => (o ? null : closeDrawer())}
        rows={allRows}
      />
    </div>
  );
}
