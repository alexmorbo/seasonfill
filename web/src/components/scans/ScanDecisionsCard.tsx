import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, CardHeader, CardContent, CardTitle } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Table, TableBody } from '@/components/ui/table';
import { Button } from '@/components/ui/button';
import { SkeletonRows } from '@/components/SkeletonRows';
import { EmptyState } from '@/components/EmptyState';
import { SeriesGroup } from '@/components/SeriesGroup';
import { OUTCOMES } from '@/lib/outcomes';
import type { SeriesGroup as SeriesGroupModel } from '@/lib/decision-grouping';

export function ScanDecisionsCard({
  groups, totalSeasons, outcome, expanded, isPending, isFetchingNext, sentinelRef,
  onOutcomeChange, onToggleSeries, onOpenDecision,
}: {
  groups: readonly SeriesGroupModel[];
  totalSeasons: number;
  outcome: string;
  expanded: ReadonlySet<string>;
  isPending: boolean;
  isFetchingNext: boolean;
  sentinelRef: React.RefObject<HTMLDivElement>;
  onOutcomeChange: (v: string) => void;
  onToggleSeries: (title: string) => void;
  onOpenDecision: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [showSkipped, setShowSkipped] = useState(false);

  // F-P1-10: hide series whose worstCategory is 'all_complete' by
  // default. A toggle pill above the list reveals them. Matches the
  // existing "default-expand when != all_complete" rule in
  // ScanDetail.tsx — same notion of "nothing to do" applies here as
  // a default hide.
  const { visible, hiddenCount } = useMemo(() => {
    let hidden = 0;
    const vis: SeriesGroupModel[] = [];
    for (const g of groups) {
      if (g.worstCategory === 'all_complete') {
        hidden += 1;
        if (showSkipped) vis.push(g);
      } else {
        vis.push(g);
      }
    }
    return { visible: vis, hiddenCount: hidden };
  }, [groups, showSkipped]);

  return (
    <Card data-testid="scan-decisions-card">
      <CardHeader className="flex flex-row items-center justify-between py-3">
        <CardTitle className="text-[14px] font-semibold">
          {t('scanDetail.decisionsCardTitle')}{' '}
          <span className="text-faint font-mono text-[11px] ml-2">
            {t('scanDetail.decisionsCardSubtitle', { series: groups.length, seasons: totalSeasons })}
          </span>
        </CardTitle>
        <div className="flex items-center gap-2">
          <span className="text-[11px] text-faint uppercase tracking-[0.06em]">
            {t('scanDetail.resultFilterLabel')}
          </span>
          <Select
            value={outcome}
            onValueChange={(v) => { if (v) onOutcomeChange(v); }}
          >
            <SelectTrigger
              className="h-7 w-[160px] text-[12px]"
              aria-label={t('scanDetail.outcomeFilterAria')}
              data-testid="scan-result-filter"
            >
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
        {isPending && (
          <Table>
            <TableBody>
              <SkeletonRows rows={8} cols={['lg', 'sm', 'md', 'xl']} />
            </TableBody>
          </Table>
        )}
        {!isPending && groups.length === 0 && (
          <EmptyState
            title={t('scanDetail.decisionsEmptyTitle')}
            body={t('scanDetail.decisionsEmptyBody')}
          />
        )}
        {hiddenCount > 0 && (
          <div className="px-4 py-2 border-b border-border-faint">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-7 px-2 text-[11px] text-muted hover:text-foreground"
              onClick={() => setShowSkipped((v) => !v)}
              data-testid="scan-decisions-skip-toggle"
              aria-pressed={showSkipped}
            >
              {showSkipped
                ? t('scanDetail.hideSkippedAllComplete', { count: hiddenCount })
                : t('scanDetail.skippedAllComplete', { count: hiddenCount })}
            </Button>
          </div>
        )}
        {visible.map((g) => (
          <SeriesGroup
            key={g.seriesId}
            group={g}
            expanded={expanded.has(g.seriesTitle)}
            onToggle={() => onToggleSeries(g.seriesTitle)}
            onOpenDecision={onOpenDecision}
          />
        ))}
        {isFetchingNext && visible.length > 0 && (
          <Table>
            <TableBody>
              <SkeletonRows rows={3} cols={['lg', 'sm', 'md', 'xl']} />
            </TableBody>
          </Table>
        )}
        <div ref={sentinelRef} aria-hidden="true" className="h-1" data-testid="scan-decisions-sentinel" />
      </CardContent>
    </Card>
  );
}
