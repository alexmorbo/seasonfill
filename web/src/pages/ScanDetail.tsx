import { useEffect, useMemo, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { ArrowLeft, AlertTriangle } from 'lucide-react';
import { DecisionDrawer } from '@/components/DecisionDrawer';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { useScan } from '@/lib/scans';
import { useDecisions, flattenDecisions } from '@/lib/decisions';
import { useGrabs, flattenGrabs } from '@/lib/grabs';
import { groupBySeries, sortGroups } from '@/lib/decision-grouping';
import { readExpanded, writeExpanded } from '@/lib/url-expand';
import { useInfiniteScroll } from '@/lib/use-infinite-scroll';
import { ScanHeaderCard } from '@/components/scans/ScanHeaderCard';
import { ScanDecisionsCard } from '@/components/scans/ScanDecisionsCard';
import { ScanLinkedGrabsCard } from '@/components/scans/ScanLinkedGrabsCard';

export function ScanDetail() {
  const { t } = useTranslation();
  useSetPageTitle(t('scanDetail.titleShort'));
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

  // Invalidate decisions + grabs once on transition into terminal so the
  // user sees the final counts immediately (preserved from legacy code).
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
  const linkedDecisions = useMemo(
    () => (id ? allDecisions.filter((d) => d.scan_run_id === id) : allDecisions),
    [allDecisions, id],
  );
  const linkedGrabs = useMemo(
    () => (id ? allGrabs.filter((g) => g.scan_run_id === id) : allGrabs),
    [allGrabs, id],
  );
  const groups = useMemo(() => sortGroups(groupBySeries(linkedDecisions)), [linkedDecisions]);

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
    if (!v || v === 'all') next.delete(k); else next.set(k, v);
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
  if (scan.isPending) {
    return <div className="max-w-[1440px] mx-auto p-6 text-muted">{t('scanDetail.loading')}</div>;
  }
  if (scan.isError || !s) {
    return (
      <div className="max-w-[1440px] mx-auto p-6">
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('scanDetail.notFoundTitle')}</AlertTitle>
          <AlertDescription>
            {scan.error?.message ?? t('scanDetail.notFoundBodyFallback')}
          </AlertDescription>
        </Alert>
      </div>
    );
  }

  return (
    <div className="max-w-[1440px] mx-auto p-6 flex flex-col gap-5">
      <Button
        variant="ghost" size="sm"
        className="self-start -ml-2 h-8"
        onClick={() => navigate('/scans')}
      >
        <ArrowLeft className="w-3.5 h-3.5 mr-1" />
        {t('scanDetail.backToScans')}
      </Button>

      <ScanHeaderCard scan={s} />

      {s.status === 'failed' && s.error_message && (
        <Alert variant="destructive">
          <AlertTriangle className="w-4 h-4" />
          <AlertTitle>{t('scanDetail.failedTitle')}</AlertTitle>
          <AlertDescription className="font-mono text-[12px]">
            {s.error_message}
          </AlertDescription>
        </Alert>
      )}

      <ScanDecisionsCard
        groups={groups}
        totalSeasons={linkedDecisions.length}
        outcome={outcome}
        expanded={expanded}
        isPending={decisions.isPending}
        isFetchingNext={decisions.isFetchingNextPage}
        sentinelRef={sentinelRef as React.RefObject<HTMLDivElement>}
        onOutcomeChange={(v) => setParam('outcome', v)}
        onToggleSeries={toggle}
        onOpenDecision={openDrawer}
      />

      <ScanLinkedGrabsCard grabs={linkedGrabs} />

      <DecisionDrawer
        id={drawerId}
        open={Boolean(drawerId)}
        onOpenChange={(o) => { if (!o) closeDrawer(); }}
        rows={linkedDecisions}
      />
    </div>
  );
}
