import { useMemo } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { ArrowLeft, Copy, AlertTriangle } from 'lucide-react';
import { toast } from 'sonner';
import { StatCard } from '@/components/StatCard';
import { StatusBadge } from '@/components/StatusBadge';
import { EmptyState } from '@/components/EmptyState';
import { SkeletonRows } from '@/components/SkeletonRows';
import { ScanProgressBar } from '@/components/ScanProgressBar';
import { SeriesGroup } from '@/components/SeriesGroup';
import { DecisionDrawer } from '@/components/DecisionDrawer';
import { OUTCOMES } from '@/components/OutcomeChips';
import { useScan } from '@/lib/scans';
import { useDecisions, flattenDecisions } from '@/lib/decisions';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { relativeTime, durationMs } from '@/lib/format';
import { groupBySeries, sortGroups } from '@/lib/decision-grouping';
import { readExpanded, writeExpanded } from '@/lib/url-expand';

export function ScanDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const outcome = params.get('outcome') ?? 'all';
  const drawerId = params.get('drawer');

  const scan = useScan(id);
  const decisions = useDecisions({
    ...(id && { scan_run_id: id }),
    ...(outcome !== 'all' && { decision: outcome }),
  });
  const grabs = useGrabs(id ? { scan_run_id: id } : {});

  const allDecisions = useMemo(() => flattenDecisions(decisions.data?.pages), [decisions.data]);
  const allGrabs = useMemo(() => flattenGrabs(grabs.data?.pages), [grabs.data]);
  // Defensive client filter — backend support pending (§22 Q-2 from 009d1).
  const linkedDecisions = useMemo(
    () => (id ? allDecisions.filter((d) => d.scan_run_id === id) : allDecisions), [allDecisions, id]);
  const linkedGrabs = useMemo(
    () => (id ? allGrabs.filter((g) => g.scan_run_id === id) : allGrabs), [allGrabs, id]);
  const groups = useMemo(() => sortGroups(groupBySeries(linkedDecisions)), [linkedDecisions]);

  // Expand-state in URL (Q-011d-3). Absent `?expanded` → synthesise
  // default (non-all_complete groups) without writing back. Once user
  // toggles, `writeExpanded` always emits the key (`expanded=` when
  // empty), so `params.has('expanded')` is the "user has interacted"
  // signal that suppresses default-expansion (r1: Bug 2).
  const urlExpanded = useMemo(() => readExpanded(params.toString()), [params]);
  const hasExplicitExpand = params.has('expanded');
  const expanded = useMemo<ReadonlySet<string>>(() => {
    if (hasExplicitExpand) return urlExpanded;
    const out = new Set<string>();
    for (const g of groups) if (g.worstCategory !== 'all_complete') out.add(g.seriesTitle);
    return out;
  }, [hasExplicitExpand, urlExpanded, groups]);

  const setParam = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (!v) next.delete(k); else next.set(k, v);
    setParams(next, { replace: true });
  };
  const toggle = (title: string) => {
    const next = new Set(expanded);
    if (next.has(title)) next.delete(title); else next.add(title);
    setParams(new URLSearchParams(writeExpanded(params.toString(), next)), { replace: true });
  };
  const openDrawer = (decId: string) => setParam('drawer', decId);
  const closeDrawer = () => setParam('drawer', '');

  const s = scan.data;
  const copy = () => {
    if (s?.id) { navigator.clipboard?.writeText(s.id); toast.success('Scan id copied'); }
  };

  if (scan.isPending) return <div className="max-w-[1440px] mx-auto p-6 text-muted">Loading scan…</div>;
  if (scan.isError || !s) return (
    <div className="max-w-[1440px] mx-auto p-6">
      <Alert variant="destructive">
        <AlertTriangle className="w-4 h-4" />
        <AlertTitle>Scan not found</AlertTitle>
        <AlertDescription>{scan.error?.message ?? 'No scan with that id.'}</AlertDescription>
      </Alert>
    </div>
  );
  const grabsOk = (s.grabs_performed ?? 0) - (s.grabs_failed ?? 0);
  const grabsFailed = s.grabs_failed ?? 0;
  const statusVariant = s.status === 'failed' ? 'danger' : s.status === 'completed' ? 'success' : 'default';

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <Button variant="ghost" size="sm" className="self-start -ml-2 h-8" onClick={() => navigate('/scans')}>
        <ArrowLeft className="w-3.5 h-3.5 mr-1" /> Back to scans
      </Button>

      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-[22px] font-semibold tracking-tight">
            Scan <span className="font-mono font-medium">{(s.id ?? '').slice(0, 8)}</span>
          </h1>
          <button onClick={copy} aria-label="Copy full scan id" className="text-muted hover:text-foreground p-1 rounded">
            <Copy className="w-3.5 h-3.5" />
          </button>
          <StatusBadge value={s.status} />
          {s.status === 'running' && (
            <span className="font-mono text-[11px] text-faint" data-testid="poll-indicator">polling every 2s</span>
          )}
        </div>
        <div className="text-[12.5px] text-muted flex items-center gap-2 flex-wrap font-mono">
          <span>{s.instance}</span><span className="text-faint">·</span>
          <span>{s.trigger}</span><span className="text-faint">·</span>
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

      {/* Q-011d-2: progress bar replaces old LiveScan card. */}
      <ScanProgressBar status={s.status} seriesScanned={s.series_scanned ?? 0}
        {...(s.started_at && { startedAt: s.started_at })}
        {...(s.finished_at && { finishedAt: s.finished_at })} />

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
        <StatCard label="Series scanned" value={s.series_scanned ?? 0} />
        <StatCard label="Candidates" value={s.candidates_found ?? 0} />
        <StatCard label="Grabs" value={grabsOk}
          {...(grabsFailed > 0 ? { suffix: `/ ${grabsFailed} fail` } : {})}
          variant={grabsFailed > 0 ? 'warning' : 'success'} />
        <StatCard label="Status" variant={statusVariant}
          value={<span className="text-[18px] lowercase">{s.status ?? '—'}</span>} />
      </div>

      <div className="text-[12px] text-muted font-mono">
        {linkedDecisions.length} decisions · {linkedGrabs.length} grabs
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
              {groups.length} series · {linkedDecisions.length} seasons
            </span>
          </CardTitle>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-faint uppercase tracking-[0.06em]">outcome</span>
            <Select value={outcome} onValueChange={(v) => setParam('outcome', v === 'all' ? '' : v)}>
              <SelectTrigger className="h-7 w-[160px] text-[12px]" aria-label="Outcome filter">
                <SelectValue placeholder="all" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">all</SelectItem>
                {OUTCOMES.map((o) => (<SelectItem key={o} value={o}>{o}</SelectItem>))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {decisions.isPending && (<div className="p-4"><SkeletonRows rows={3} cols={['lg', 'sm', 'md', 'xl']} /></div>)}
          {!decisions.isPending && groups.length === 0 && (
            <EmptyState title="No decisions for this scan"
              body="Either the scan made no decisions or none match the current filter." />
          )}
          {groups.map((g) => (
            <SeriesGroup key={g.seriesId} group={g}
              expanded={expanded.has(g.seriesTitle)}
              onToggle={() => toggle(g.seriesTitle)} onOpenDecision={openDrawer} />
          ))}
        </CardContent>
      </Card>

      {/* Linked grabs card preserved verbatim from current ScanDetail.tsx 282-328. */}
      {linkedGrabs.length > 0 && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between py-3">
            <CardTitle className="text-[14px] font-semibold">Linked grabs</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>All grabs →</Button>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Release</TableHead><TableHead>Status</TableHead>
                  <TableHead>Indexer</TableHead><TableHead>Updated</TableHead>
                  <TableHead>Attempts</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {linkedGrabs.map((g) => (
                  <TableRow key={g.id} tabIndex={0} role="button"
                    onClick={() => g.id && navigate(`/grabs?drawer=${encodeURIComponent(g.id)}`)}
                    aria-label={`Open grab ${g.release_title ?? g.id}`}
                    className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <TableCell className="font-mono text-[12px] max-w-md truncate">{g.release_title ?? '—'}</TableCell>
                    <TableCell><StatusBadge value={g.status} /></TableCell>
                    <TableCell className="font-mono text-muted">{g.indexer_name ?? '—'}</TableCell>
                    <TableCell className="text-muted">{relativeTime(g.updated_at ?? g.created_at)}</TableCell>
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
          <Button variant="outline" onClick={() => decisions.fetchNextPage()} disabled={decisions.isFetchingNextPage}>
            {decisions.isFetchingNextPage ? 'Loading…' : 'Load more decisions'}
          </Button>
        </div>
      )}

      <DecisionDrawer
        id={drawerId}
        open={Boolean(drawerId)}
        onOpenChange={(o) => { if (!o) closeDrawer(); }}
        rows={linkedDecisions}
      />
    </div>
  );
}
