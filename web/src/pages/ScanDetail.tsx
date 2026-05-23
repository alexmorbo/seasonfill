import { useMemo, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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
import { ArrowLeft, Copy, AlertTriangle, ChevronRight, Activity } from 'lucide-react';
import { toast } from 'sonner';
import { Progress } from '@/components/ui/progress';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { CategoryChip } from '@/components/CategoryChip';
import { EmptyState } from '@/components/EmptyState';
import { SkeletonRows } from '@/components/SkeletonRows';
import { DecisionDetail } from '@/components/DecisionDetail';
import { OUTCOMES } from '@/components/OutcomeChips';
import { useScan } from '@/lib/scans';
import { useDecisions, flattenDecisions } from '@/lib/decisions';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { relativeTime, durationMs } from '@/lib/format';
import { cn } from '@/lib/utils';

export function ScanDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [outcome, setOutcome] = useState<string>('all');
  const [expanded, setExpanded] = useState<string | null>(null);

  const scan = useScan(id);
  const decisions = useDecisions({
    ...(id && { scan_run_id: id }),
    ...(outcome !== 'all' && { decision: outcome }),
  });
  const grabs = useGrabs(id ? { scan_run_id: id } : {});

  const allDecisions = useMemo(() => flattenDecisions(decisions.data?.pages), [decisions.data]);
  const allGrabs = useMemo(() => flattenGrabs(grabs.data?.pages), [grabs.data]);
  // Defensive client filter — backend support pending (§22 Q-2).
  const linkedDecisions = useMemo(
    () => (id ? allDecisions.filter((d) => d.scan_run_id === id) : allDecisions),
    [allDecisions, id],
  );
  const linkedGrabs = useMemo(
    () => (id ? allGrabs.filter((g) => g.scan_run_id === id) : allGrabs),
    [allGrabs, id],
  );

  const s = scan.data;
  const copy = () => {
    if (s?.id) {
      navigator.clipboard?.writeText(s.id);
      toast.success('Scan id copied');
    }
  };

  if (scan.isPending) {
    return <div className="max-w-[1440px] mx-auto p-6 text-muted">Loading scan…</div>;
  }
  if (scan.isError || !s) {
    return (
      <div className="max-w-[1440px] mx-auto p-6">
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>Scan not found</AlertTitle>
          <AlertDescription>{scan.error?.message ?? 'No scan with that id.'}</AlertDescription>
        </Alert>
      </div>
    );
  }
  const grabsOk = (s.grabs_performed ?? 0) - (s.grabs_failed ?? 0);
  const grabsFailed = s.grabs_failed ?? 0;
  const statusVariant =
    s.status === 'failed' ? 'danger' : s.status === 'completed' ? 'success' : 'default';

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <Button
        variant="ghost"
        size="sm"
        className="self-start -ml-2 h-8"
        onClick={() => navigate('/scans')}
      >
        <ArrowLeft className="w-3.5 h-3.5 mr-1" /> Back to scans
      </Button>

      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-[22px] font-semibold tracking-tight">
            Scan <span className="font-mono font-medium">{(s.id ?? '').slice(0, 8)}</span>
          </h1>
          <button
            onClick={copy}
            aria-label="Copy full scan id"
            className="text-muted hover:text-foreground p-1 rounded"
          >
            <Copy className="w-3.5 h-3.5" />
          </button>
          <StatusBadge value={s.status} />
          {s.status === 'running' && (
            <span className="font-mono text-[11px] text-faint">polling every 5s</span>
          )}
        </div>
        <div className="text-[12.5px] text-muted flex items-center gap-2 flex-wrap font-mono">
          <span>{s.instance}</span>
          <span className="text-faint">·</span>
          <span>{s.trigger}</span>
          <span className="text-faint">·</span>
          <span>{relativeTime(s.started_at)}</span>
          {s.finished_at && (
            <>
              <span className="text-faint">→</span>
              <span>{relativeTime(s.finished_at)}</span>
              <span className="text-faint">({durationMs(s.started_at, s.finished_at)})</span>
            </>
          )}
        </div>
      </header>

      {s.status === 'running' && (
        <Card aria-label="Live scan progress">
          <CardHeader className="flex flex-row items-center justify-between py-3">
            <CardTitle className="text-[13px] font-semibold flex items-center gap-2">
              <Activity className="w-3.5 h-3.5 text-accent animate-pulse" />
              Live scan
            </CardTitle>
            <span className="font-mono text-[11px] text-faint">
              {s.series_scanned ?? 0} series scanned ·{' '}
              {s.started_at
                ? `${Math.max(0, (Date.now() - new Date(s.started_at).getTime()) / 1000).toFixed(0)}s elapsed`
                : '—'}
            </span>
          </CardHeader>
          <CardContent className="pt-0 pb-4 px-4 flex flex-col gap-3">
            {/* Indeterminate stripe — backend has no total_series. */}
            <Progress
              aria-label="Scan in progress (indeterminate)"
              className="h-1.5 bg-surface-2 overflow-hidden relative animate-pulse"
            />
            {linkedDecisions.length > 0 && (
              <div className="flex flex-col gap-1 max-h-[180px] overflow-y-auto">
                <span className="text-[10px] uppercase tracking-[0.06em] text-faint">
                  Latest decisions
                </span>
                {linkedDecisions.slice(0, 10).map((d) => (
                  <div
                    key={d.id}
                    className="flex items-center gap-2 text-[12px] font-mono px-2 py-1 rounded bg-surface"
                  >
                    <span className="text-faint shrink-0">{relativeTime(d.created_at)}</span>
                    <span className="truncate flex-1">{d.series_title ?? '—'}</span>
                    <CategoryChip value={d.category} variant="compact" />
                    <StatusBadge value={d.decision} mode="outcome" />
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard label="Series scanned" value={s.series_scanned ?? 0} />
        <StatCard label="Candidates" value={s.candidates_found ?? 0} />
        <StatCard
          label="Grabs"
          value={grabsOk}
          {...(grabsFailed > 0 ? { suffix: `/ ${grabsFailed} fail` } : {})}
          variant={grabsFailed > 0 ? 'warning' : 'success'}
        />
        <StatCard
          label="Status"
          value={<span className="text-[18px] lowercase">{s.status ?? '—'}</span>}
          variant={statusVariant}
        />
      </div>

      {s.status === 'failed' && s.error_message && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>Scan failed</AlertTitle>
          <AlertDescription className="font-mono text-[12px]">{s.error_message}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">
            Decisions{' '}
            <span className="text-faint font-mono text-[11px] ml-2">
              {linkedDecisions.length} loaded
            </span>
          </CardTitle>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-faint uppercase tracking-[0.06em]">outcome</span>
            <Select value={outcome} onValueChange={setOutcome}>
              <SelectTrigger className="h-7 w-[160px] text-[12px]" aria-label="Outcome filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">all</SelectItem>
                {OUTCOMES.map((o) => (
                  <SelectItem key={o} value={o}>
                    {o}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {decisions.isPending && (
            <div className="p-4">
              <SkeletonRows rows={3} cols={['lg', 'sm', 'md', 'xl']} />
            </div>
          )}
          {!decisions.isPending && linkedDecisions.length === 0 && (
            <EmptyState
              title="No decisions for this scan"
              body="Either the scan made no decisions or none match the current filter."
            />
          )}
          {linkedDecisions.map((d) => {
            const isOpen = expanded === d.id;
            return (
              <div key={d.id} className="border-b border-border-faint last:border-b-0">
                <button
                  type="button"
                  onClick={() => setExpanded(isOpen ? null : (d.id ?? null))}
                  aria-expanded={isOpen}
                  aria-controls={`dec-body-${d.id}`}
                  className={cn(
                    'w-full flex items-center gap-3 px-4 py-3 text-left transition focus:outline-none focus-visible:bg-surface-2',
                    isOpen ? 'bg-surface-2' : 'hover:bg-surface-2',
                  )}
                >
                  <ChevronRight
                    className={cn(
                      'w-3.5 h-3.5 text-muted transition-transform',
                      isOpen && 'rotate-90',
                    )}
                  />
                  <span className="font-medium min-w-[200px]">
                    {d.series_title ?? '—'}{' '}
                    {d.season_number !== undefined && (
                      <span className="font-mono text-[12px] text-muted">
                        S{String(d.season_number).padStart(2, '0')}
                      </span>
                    )}
                  </span>
                  <CategoryChip value={d.category} variant="compact" />
                  <StatusBadge value={d.decision} mode="outcome" />
                  <span className="text-[12px] text-muted truncate flex-1">{d.reason ?? ''}</span>
                  <span className="text-[11px] text-faint font-mono">
                    {d.candidates_count ?? 0} cand.
                  </span>
                </button>
                {isOpen && (
                  <div id={`dec-body-${d.id}`} className="px-12 pb-4">
                    <DecisionDetail d={d} />
                  </div>
                )}
              </div>
            );
          })}
        </CardContent>
      </Card>

      {linkedGrabs.length > 0 && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between py-3">
            <CardTitle className="text-[14px] font-semibold">Linked grabs</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>
              All grabs →
            </Button>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Release</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Indexer</TableHead>
                  <TableHead>Updated</TableHead>
                  <TableHead>Attempts</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {linkedGrabs.map((g) => (
                  <TableRow
                    key={g.id}
                    onClick={() => g.id && navigate(`/grabs?drawer=${encodeURIComponent(g.id)}`)}
                    tabIndex={0}
                    role="button"
                    aria-label={`Open grab ${g.release_title ?? g.id}`}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <TableCell className="font-mono text-[12px] max-w-md truncate">
                      {g.release_title ?? '—'}
                    </TableCell>
                    <TableCell>
                      <StatusBadge value={g.status} />
                    </TableCell>
                    <TableCell className="font-mono text-muted">{g.indexer_name ?? '—'}</TableCell>
                    <TableCell className="text-muted">
                      {relativeTime(g.updated_at ?? g.created_at)}
                    </TableCell>
                    <TableCell className="font-mono">{g.attempts ?? 0}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {decisions.hasNextPage && (
        <div className="flex justify-center">
          <Button
            variant="outline"
            onClick={() => decisions.fetchNextPage()}
            disabled={decisions.isFetchingNextPage}
          >
            {decisions.isFetchingNextPage ? 'Loading…' : 'Load more decisions'}
          </Button>
        </div>
      )}
    </div>
  );
}
