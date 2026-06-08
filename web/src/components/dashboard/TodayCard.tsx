import { TriangleAlert, TrendingUp, TrendingDown, Minus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Sparkline } from '@/components/ui/sparkline';
import { useCountersAggregate, sumTotals, sumAvgGrabs7d, rollupDailyGrabs } from '@/lib/api/counters';
import { cn } from '@/lib/utils';

// Trend per 049 index risk: >=1.2x avg = up; <=0.8x = down; else flat. flat covers silence.
export type Trend = 'up' | 'down' | 'flat';
// reason: classifyTrend is the pure function under test; co-located with
// the component that consumes it. Splitting into a separate file just to
// satisfy the HMR rule is a worse design.
// eslint-disable-next-line react-refresh/only-export-components
export function classifyTrend(today: number, avg: number): Trend {
  if (avg < 1 && today === 0) return 'flat';
  if (avg < 0.5) return today === 0 ? 'flat' : 'up';
  const r = today / avg;
  return r >= 1.2 ? 'up' : r <= 0.8 ? 'down' : 'flat';
}

const DAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;
const TREND_ICON = { up: TrendingUp, down: TrendingDown, flat: Minus } as const;
const TREND_KEY: Record<Trend, 'trendUp' | 'trendDown' | 'trendFlat'> = { up: 'trendUp', down: 'trendDown', flat: 'trendFlat' };
const TREND_COLOR: Record<Trend, string> = { up: 'text-ok', down: 'text-warn', flat: 'text-tx-muted' };

export function TodayCard() {
  const { t } = useTranslation();
  const dayQ = useCountersAggregate('24h');
  const weekQ = useCountersAggregate('7d');
  const hasError = dayQ.isError || weekQ.isError;

  if (dayQ.isPending || weekQ.isPending) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-baseline justify-between p-4 pb-2">
          <CardTitle className="text-sm font-semibold">{t('dashboard.rail.today.label')}</CardTitle>
          <span className="text-xs text-tx-faint">{t('dashboard.rail.today.vsAvg')}</span>
        </CardHeader>
        <CardContent className="flex flex-col gap-3 p-4 pt-0">
          <Skeleton className="h-8 w-24" /><Skeleton className="h-2 w-full" /><Skeleton className="h-9 w-full" />
        </CardContent>
      </Card>
    );
  }

  const totals = sumTotals(dayQ.data);
  const avg = sumAvgGrabs7d(weekQ.data);
  const trend = hasError ? 'flat' : classifyTrend(totals.grabs, avg);
  const spark = rollupDailyGrabs(weekQ.data);
  const tot = totals.grabs + totals.imports + totals.fails;
  const w = (n: number) => tot > 0 ? (n / tot) * 100 : 0;
  const TrendIcon = TREND_ICON[trend];

  return (
    <Card data-testid="today-card">
      <CardHeader className="flex flex-row items-baseline justify-between p-4 pb-2">
        <CardTitle className="flex items-center gap-2 text-sm font-semibold">
          {t('dashboard.rail.today.label')}
          {hasError && <TriangleAlert className="h-3.5 w-3.5 text-warn" aria-label={t('dashboard.rail.loadFailed')} data-testid="today-load-failed" />}
        </CardTitle>
        <span className="text-xs text-tx-faint">{t('dashboard.rail.today.vsAvg')}</span>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 p-4 pt-0">
        <div className="flex items-baseline gap-3">
          <span className={cn('font-mono text-3xl font-bold tabular-nums leading-none', hasError && 'text-tx-faint')} data-testid="today-big-n">
            {hasError ? '—' : totals.grabs}
          </span>
          <span className="text-xs text-tx-muted">{t('dashboard.rail.today.split.grabs')}</span>
          <span className="flex-1" />
          <span className="flex gap-2 text-xs text-tx-muted">
            <span><b className="font-mono font-semibold text-tx-secondary">{totals.imports}</b> {t('dashboard.rail.today.split.imports')}</span>
            <span><b className={cn('font-mono font-semibold', totals.fails > 0 ? 'text-danger' : 'text-tx-secondary')}>{totals.fails}</b> {t('dashboard.rail.today.split.fails')}</span>
          </span>
        </div>
        <div className="flex h-2 overflow-hidden rounded-md bg-bg-surface-2" data-testid="density-bar">
          <span className="block h-full bg-accent" style={{ width: `${w(totals.grabs)}%` }} />
          <span className="block h-full bg-accent/55" style={{ width: `${w(totals.imports)}%` }} data-seg="imp" />
          <span className="block h-full bg-danger" style={{ width: `${w(totals.fails)}%` }} data-seg="fail" />
        </div>
        <div className={cn('flex items-center gap-1.5 text-xs', TREND_COLOR[trend])} data-testid="trend-chip" data-trend={trend}>
          <TrendIcon className="h-3.5 w-3.5" />
          <span>{t(`dashboard.rail.today.${TREND_KEY[trend]}`, { avg: Math.round(avg) })}</span>
        </div>
        <div>
          <Sparkline data={spark} ariaLabel={t('dashboard.rail.today.spark.aria')} className="h-9" />
          <div className="mt-1 flex justify-between font-mono text-[9.5px] text-tx-faint">
            {DAYS.map((d) => <span key={d}>{t(`dashboard.rail.today.spark.days.${d}`)}</span>)}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
