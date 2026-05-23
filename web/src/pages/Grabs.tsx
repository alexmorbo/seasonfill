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
import { SkeletonRows } from '@/components/SkeletonRows';
import { EmptyState } from '@/components/EmptyState';
import { GrabDrawer } from '@/components/GrabDrawer';
import { useGrabs, flattenGrabs, type GrabFilters } from '@/lib/grabs';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime } from '@/lib/format';

const STATUSES = ['grabbed', 'imported', 'import_failed', 'grab_failed', 'expired'] as const;

export function Grabs() {
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();
  const status = params.get('status') ?? '';
  const q = params.get('q') ?? '';
  const drawer = params.get('drawer');

  const queryFilters: GrabFilters = useMemo(() => ({ ...(status && { status }) }), [status]);
  const query = useGrabs(queryFilters);
  const allRows = useMemo(() => flattenGrabs(query.data?.pages), [query.data]);
  const rows = useMemo(() => {
    if (!q) return allRows;
    const needle = q.toLowerCase();
    return allRows.filter(
      (g) =>
        (g.series_title ?? '').toLowerCase().includes(needle) ||
        (g.release_title ?? '').toLowerCase().includes(needle),
    );
  }, [allRows, q]);

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
        <h1 className="text-[22px] font-semibold tracking-tight">Grabs</h1>
        <span className="font-mono text-[12px] text-faint">
          {rows.length} loaded{instance ? ` · ${instance}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">Filter</span>
        <Select
          value={status || 'all'}
          onValueChange={(v) => setParam('status', v === 'all' ? '' : v)}
        >
          <SelectTrigger className="h-8 w-[160px] text-[12.5px]" aria-label="Any status">
            <SelectValue placeholder="Any status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">Any status</SelectItem>
            {STATUSES.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          placeholder="Search series or release…"
          value={q}
          onChange={(e) => setParam('q', e.target.value)}
          className="h-8 w-[260px] text-[12.5px]"
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
        <Button variant="ghost" size="sm" onClick={clear} disabled={!status && !q}>
          Clear
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {query.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>Failed to load grabs</AlertTitle>
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
                  <TableHead>Release</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Indexer</TableHead>
                  <TableHead>Attempts</TableHead>
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
                        title="No grabs match"
                        body="Try clearing filters."
                        {...(status || q
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
                {rows.map((g) => (
                  <TableRow
                    key={g.id}
                    onClick={() => openDrawer(g.id)}
                    onKeyDown={(e) => onKey(e, g.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={`Open grab ${g.id ?? ''}`}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
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
            {query.isFetchingNextPage ? 'Loading…' : 'Show more'}
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
