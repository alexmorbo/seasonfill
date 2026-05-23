import { useMemo } from 'react';
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
import { OutcomeChips, OUTCOMES, type Outcome } from '@/components/OutcomeChips';
import { DecisionDrawer } from '@/components/DecisionDrawer';
import { useDecisions, flattenDecisions, type DecisionFilters } from '@/lib/decisions';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime } from '@/lib/format';

export function Decisions() {
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
  const rows = useMemo(
    () =>
      allRows.filter((d) => {
        if (selected.size > 0 && d.decision && !selected.has(d.decision as Outcome)) return false;
        if (q && !(d.series_title ?? '').toLowerCase().includes(q.toLowerCase())) return false;
        return true;
      }),
    [allRows, selected, q],
  );

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
        <h1 className="text-[22px] font-semibold tracking-tight">Decisions</h1>
        <span className="font-mono text-[12px] text-faint">
          {rows.length} loaded{instance ? ` · ${instance}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">Filter</span>
        <Input
          placeholder="Search series…"
          value={q}
          onChange={(e) => setParam('q', e.target.value)}
          className="h-8 w-[220px] text-[12.5px]"
        />
        <Select defaultValue="24h">
          <SelectTrigger className="h-8 w-[120px] text-[12.5px]" aria-label="Time range (decorative)">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="24h">Last 24h</SelectItem>
            <SelectItem value="7d">Last 7d</SelectItem>
            <SelectItem value="30d">Last 30d</SelectItem>
          </SelectContent>
        </Select>
        <div className="flex-1" />
        <Button variant="ghost" size="sm" onClick={clear} disabled={selected.size === 0 && !q}>
          Clear
        </Button>
      </div>

      <OutcomeChips selected={selected} onToggle={toggleOutcome} />
      {selected.size > 1 && (
        <p className="text-[11px] text-muted -mt-1 font-mono">
          Backend accepts one outcome at a time; extras filter the loaded page client-side.
        </p>
      )}

      <Card>
        <CardContent className="p-0">
          {query.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>Failed to load decisions</AlertTitle>
              <AlertDescription>
                {query.error.message}{' '}
                <Button variant="link" size="sm" onClick={() => query.refetch()}>
                  Retry
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Time</TableHead>
                  <TableHead>Instance</TableHead>
                  <TableHead>Series</TableHead>
                  <TableHead>Season</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead>Category</TableHead>
                  <TableHead>Reason</TableHead>
                  <TableHead>Cand</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {query.isPending && (
                  <SkeletonRows
                    rows={6}
                    cols={['sm', 'md', 'lg', 'sm', 'md', 'md', 'xl', 'sm']}
                  />
                )}
                {!query.isPending && rows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={8}>
                      <EmptyState
                        title="No decisions match"
                        body="Adjust filters or wait for the next scan."
                        {...(selected.size > 0 || q
                          ? {
                              action: (
                                <Button variant="outline" size="sm" onClick={clear}>
                                  Clear filters
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
                    aria-label={`Open decision ${d.id ?? ''}`}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
                      {d.reason ?? '—'}
                    </TableCell>
                    <TableCell className="font-mono">{d.candidates_count ?? 0}</TableCell>
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
            {query.isFetchingNextPage ? 'Loading…' : 'Show more'}
          </Button>
        </div>
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
