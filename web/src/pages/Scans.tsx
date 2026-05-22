import { useMemo } from 'react';
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
import { useScans, flattenScans, type ScanFilters } from '@/lib/scans';
import { useInstanceFilter } from '@/lib/instance-filter-context-internal';
import { relativeTime, durationMs } from '@/lib/format';

const STATUSES = ['running', 'completed', 'failed', 'aborted'] as const;

export function Scans() {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const { filter: instance } = useInstanceFilter();

  const status = params.get('status') ?? '';
  const queryFilters: ScanFilters = useMemo(
    () => ({ ...(status && { status }) }),
    [status],
  );

  const q = useScans(queryFilters);
  const rows = useMemo(() => flattenScans(q.data?.pages), [q.data]);

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
        <h1 className="text-[22px] font-semibold tracking-tight">Scans</h1>
        <span className="font-mono text-[12px] text-faint">
          {rows.length} loaded{instance ? ` · instance: ${instance}` : ''}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] uppercase tracking-[0.06em] text-faint mr-1">Filter</span>
        <Select
          value={status || 'all'}
          onValueChange={(v) => updateParam('status', v === 'all' ? '' : v)}
        >
          <SelectTrigger className="h-8 w-[140px] text-[12.5px]" aria-label="Any status">
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
        <Button variant="ghost" size="sm" onClick={clear} disabled={!status}>
          Clear
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {q.isError ? (
            <Alert variant="destructive" className="m-4">
              <AlertTriangle className="w-4 h-4" />
              <AlertTitle>Failed to load scans</AlertTitle>
              <AlertDescription>
                {q.error.message}{' '}
                <Button variant="link" size="sm" onClick={() => q.refetch()}>
                  Retry
                </Button>
              </AlertDescription>
            </Alert>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8"></TableHead>
                  <TableHead>ID</TableHead>
                  <TableHead>Instance</TableHead>
                  <TableHead>Trigger</TableHead>
                  <TableHead>Started</TableHead>
                  <TableHead>Dur</TableHead>
                  <TableHead>Series</TableHead>
                  <TableHead>Grabs</TableHead>
                  <TableHead>Status</TableHead>
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
                        title="No scans match your filters"
                        body="Try clearing filters, or trigger a new scan from the sidebar."
                        {...(status
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
                {rows.map((s) => (
                  <TableRow
                    key={s.id}
                    onClick={() => s.id && navigate(`/scans/${s.id}`)}
                    onKeyDown={(e) => handleRowKey(e, s.id)}
                    tabIndex={0}
                    role="button"
                    aria-label={`Open scan ${(s.id ?? '').slice(0, 8)}`}
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
            {q.isFetchingNextPage ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
    </div>
  );
}
