import { useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
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
import { CancelScanDialog } from '@/components/CancelScanDialog';
import { OUTCOMES } from '@/components/OutcomeChips';
import { useScan } from '@/lib/scans';
import { useDecisions, flattenDecisions } from '@/lib/decisions';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { relativeTime, durationMs } from '@/lib/format';
import { groupBySeries, sortGroups } from '@/lib/decision-grouping';
import { readExpanded, writeExpanded } from '@/lib/url-expand';
import { useInfiniteScroll } from '@/lib/use-infinite-scroll';

export function ScanDetail() {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const outcome = params.get('outcome') ?? 'all';
  const drawerId = params.get('drawer');

  const scan = useScan(id);
  // While the scan is running, decisions + grabs land in pulses — poll
  // fast (2s) instead of the default 30s/none. Boolean opt-in keeps the
  // queryKey stable so cache hits survive the running→completed transition.
  const fastPoll = scan.data?.status === 'running';
  const decisions = useDecisions({
    ...(id && { scan_run_id: id }),
    ...(outcome !== 'all' && { decision: outcome }),
  }, { fastPoll });
  const grabs = useGrabs(id ? { scan_run_id: id } : {}, { fastPoll });

  // When the scan transitions into a terminal status the polling cadence
  // for decisions/grabs flips from 2s to 30s/none. The last decision
  // written in the final ~2s of the scan can land AFTER the last fast
  // poll and BEFORE the slow tick, leaving the UI stale for up to 30s
  // (observed: 4 decisions written, 3 shown for 16 min). Invalidate
  // decisions + grabs once on transition into terminal so the user sees
  // the final counts immediately.
  const queryClient = useQueryClient();
  const lastStatusRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    const current = scan.data?.status;
    const prior = lastStatusRef.current;
    if (current && current !== prior) {
      lastStatusRef.current = current;
      const isTerminal =
        current === 'completed' || current === 'failed' ||
        current === 'aborted' || current === 'cancelled';
      if (isTerminal) {
        queryClient.invalidateQueries({ queryKey: ['decisions'] });
        queryClient.invalidateQueries({ queryKey: ['grabs'] });
      }
    }
  }, [scan.data?.status, queryClient]);

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

  const { sentinelRef } = useInfiniteScroll({
    hasNextPage: decisions.hasNextPage,
    isFetchingNextPage: decisions.isFetchingNextPage,
    fetchNextPage: decisions.fetchNextPage,
  });

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
    if (s?.id) { navigator.clipboard?.writeText(s.id); toast.success(t('scanDetail.copyScanIdToast')); }
  };

  if (scan.isPending) return <div className="max-w-[1440px] mx-auto p-6 text-muted">{t('scanDetail.loading')}</div>;
  if (scan.isError || !s) return (
    <div className="max-w-[1440px] mx-auto p-6">
      <Alert variant="destructive">
        <AlertTriangle className="w-4 h-4" />
        <AlertTitle>{t('scanDetail.notFoundTitle')}</AlertTitle>
        <AlertDescription>{scan.error?.message ?? t('scanDetail.notFoundBodyFallback')}</AlertDescription>
      </Alert>
    </div>
  );
  const grabsOk = (s.grabs_performed ?? 0) - (s.grabs_failed ?? 0);
  const grabsFailed = s.grabs_failed ?? 0;
  const statusVariant = s.status === 'failed' ? 'danger' : s.status === 'completed' ? 'success' : 'default';

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <Button variant="ghost" size="sm" className="self-start -ml-2 h-8" onClick={() => navigate('/scans')}>
        <ArrowLeft className="w-3.5 h-3.5 mr-1" /> {t('scanDetail.backToScans')}
      </Button>

      <header className="flex flex-col gap-1.5">
        <div className="flex items-center gap-3 flex-wrap">
          <h1 className="text-[22px] font-semibold tracking-tight">
            {t('scanDetail.scanIdLabel')} <span className="font-mono font-medium">{(s.id ?? '').slice(0, 8)}</span>
          </h1>
          <button onClick={copy} aria-label={t('scanDetail.copyScanId')} className="text-muted hover:text-foreground p-1 rounded">
            <Copy className="w-3.5 h-3.5" />
          </button>
          <StatusBadge value={s.status} />
          {s.status === 'running' && (
            <>
              <span className="font-mono text-[11px] text-faint" data-testid="poll-indicator">{t('scanDetail.pollIndicator')}</span>
              {s.id && <CancelScanDialog scanId={s.id} />}
            </>
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
        <StatCard label={t('scanDetail.statSeriesScanned')} value={s.series_scanned ?? 0} />
        <StatCard label={t('scanDetail.statCandidates')} value={s.candidates_found ?? 0} />
        <StatCard label={t('scanDetail.statGrabs')} value={grabsOk}
          {...(grabsFailed > 0 ? { suffix: t('scanDetail.grabsFailSuffix', { count: grabsFailed }) } : {})}
          variant={grabsFailed > 0 ? 'warning' : 'success'} />
        <StatCard label={t('scanDetail.statStatus')} variant={statusVariant}
          value={<span className="text-[18px] lowercase">{s.status ?? '—'}</span>} />
      </div>

      <div className="text-[12px] text-muted font-mono">
        {t('scanDetail.counter', { decisions: linkedDecisions.length, grabs: linkedGrabs.length })}
      </div>

      {s.status === 'failed' && s.error_message && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('scanDetail.failedTitle')}</AlertTitle>
          <AlertDescription className="font-mono text-[12px]">{s.error_message}</AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader className="flex flex-row items-center justify-between py-3">
          <CardTitle className="text-[14px] font-semibold">
            {t('scanDetail.decisionsCardTitle')}{' '}
            <span className="text-faint font-mono text-[11px] ml-2">
              {t('scanDetail.decisionsCardSubtitle', { series: groups.length, seasons: linkedDecisions.length })}
            </span>
          </CardTitle>
          <div className="flex items-center gap-2">
            <span className="text-[11px] text-faint uppercase tracking-[0.06em]">{t('decisions.columns.outcome').toLowerCase()}</span>
            <Select value={outcome} onValueChange={(v) => setParam('outcome', v === 'all' ? '' : v)}>
              <SelectTrigger className="h-7 w-[160px] text-[12px]" aria-label={t('scanDetail.outcomeFilterAria')}>
                <SelectValue placeholder={t('scanDetail.outcomeFilterAll')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t('scanDetail.outcomeFilterAll')}</SelectItem>
                {OUTCOMES.map((o) => (
                  <SelectItem key={o} value={o}>
                    {t(`outcomes.${o}`, { defaultValue: o })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {decisions.isPending && (
            <Table>
              <TableBody>
                <SkeletonRows rows={8} cols={['lg', 'sm', 'md', 'xl']} />
              </TableBody>
            </Table>
          )}
          {!decisions.isPending && groups.length === 0 && (
            <EmptyState title={t('scanDetail.decisionsEmptyTitle')}
              body={t('scanDetail.decisionsEmptyBody')} />
          )}
          {groups.map((g) => (
            <SeriesGroup key={g.seriesId} group={g}
              expanded={expanded.has(g.seriesTitle)}
              onToggle={() => toggle(g.seriesTitle)} onOpenDecision={openDrawer} />
          ))}
          {decisions.isFetchingNextPage && groups.length > 0 && (
            <Table>
              <TableBody>
                <SkeletonRows rows={3} cols={['lg', 'sm', 'md', 'xl']} />
              </TableBody>
            </Table>
          )}
          {decisions.hasNextPage && (
            <div ref={sentinelRef} aria-hidden="true" className="h-1" />
          )}
        </CardContent>
      </Card>

      {/* Linked grabs card preserved verbatim from current ScanDetail.tsx 282-328. */}
      {linkedGrabs.length > 0 && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between py-3">
            <CardTitle className="text-[14px] font-semibold">{t('scanDetail.linkedGrabsTitle')}</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => navigate('/grabs')}>{t('scanDetail.linkedGrabsAllLink')}</Button>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('scanDetail.linkedColRelease')}</TableHead>
                  <TableHead>{t('scanDetail.linkedColStatus')}</TableHead>
                  <TableHead>{t('scanDetail.linkedColIndexer')}</TableHead>
                  <TableHead>{t('scanDetail.linkedColUpdated')}</TableHead>
                  <TableHead>{t('scanDetail.linkedColAttempts')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {linkedGrabs.map((g) => (
                  <TableRow key={g.id} tabIndex={0} role="button"
                    onClick={() => g.id && navigate(`/grabs?drawer=${encodeURIComponent(g.id)}`)}
                    aria-label={t('scanDetail.openGrabAria', { title: g.release_title ?? g.id ?? '' })}
                    className="cursor-pointer focus:outline-hidden focus-visible:ring-2 focus-visible:ring-ring">
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

      <DecisionDrawer
        id={drawerId}
        open={Boolean(drawerId)}
        onOpenChange={(o) => { if (!o) closeDrawer(); }}
        rows={linkedDecisions}
      />
    </div>
  );
}
