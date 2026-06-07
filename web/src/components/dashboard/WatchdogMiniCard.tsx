import { Shield, Eye, RotateCw, Ban, ArrowRight, TriangleAlert } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { useWatchdogRollups, rollupChipStatus, sumRollupTotals } from '@/lib/api/watchdogRollups';
import { cn } from '@/lib/utils';

const CHIP_VARIANT = { running: 'ok', off: 'warn', unreachable: 'destructive' } as const;

export function WatchdogMiniCard() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const q = useWatchdogRollups();

  if (q.isPending) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-baseline gap-2 p-4 pb-2">
          <Shield className="h-4 w-4 text-accent" />
          <CardTitle className="text-sm font-semibold">{t('dashboard.rail.watchdog.title')}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2.5 p-3 pt-0">
          <Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-full" />
        </CardContent>
      </Card>
    );
  }

  const chip = rollupChipStatus(q.data);
  const totals = sumRollupTotals(q.data);

  return (
    <Card data-testid="watchdog-mini-card">
      <CardHeader className="flex flex-row items-baseline justify-between p-4 pb-2">
        <span className="flex items-center gap-2">
          <Shield className="h-4 w-4 text-accent" />
          <CardTitle className="text-sm font-semibold">{t('dashboard.rail.watchdog.title')}</CardTitle>
          {q.isError && <TriangleAlert className="h-3.5 w-3.5 text-warn" aria-label={t('dashboard.rail.loadFailed')} data-testid="watchdog-load-failed" />}
        </span>
        <Badge variant={CHIP_VARIANT[chip]} mono data-testid="watchdog-chip" data-chip={chip}>{t(`dashboard.rail.watchdog.chip.${chip}`)}</Badge>
      </CardHeader>
      <CardContent className="flex flex-col gap-2.5 p-3 pt-0">
        <WdRow testId="wd-row-watched" icon={<Eye className="h-3.5 w-3.5" />} label={t('dashboard.rail.watchdog.row.watched')} value={t('dashboard.rail.watchdog.row.watchedValue', { count: totals.watched })} />
        <WdRow testId="wd-row-regrab7d" icon={<RotateCw className="h-3.5 w-3.5" />} label={t('dashboard.rail.watchdog.row.regrab7d')} value={String(totals.regrabs_7d)} />
        <WdRow testId="wd-row-blacklist" icon={<Ban className="h-3.5 w-3.5" />} label={t('dashboard.rail.watchdog.row.blacklist')} value={String(totals.blacklist_size)} valueWarn={totals.blacklist_size > 0} />
        <button type="button" onClick={() => navigate('/watchdog')} data-testid="watchdog-open"
                className="mt-1 flex w-full items-center justify-center gap-2 rounded-md border border-border-faint px-3 py-1.5 text-xs font-semibold text-tx-secondary transition-colors hover:bg-bg-surface-2 hover:text-tx-primary">
          <ArrowRight className="h-3.5 w-3.5" />{t('dashboard.rail.watchdog.open')}
        </button>
      </CardContent>
    </Card>
  );
}

interface WdRowProps { icon: React.ReactNode; label: string; value: string; valueWarn?: boolean; testId: string }
function WdRow({ icon, label, value, valueWarn, testId }: WdRowProps) {
  return (
    <div className="flex items-center gap-2 text-xs text-tx-muted" data-testid={testId}>
      <span className="text-tx-faint">{icon}</span><span>{label}</span>
      <b className={cn('ml-auto font-mono font-semibold', valueWarn ? 'text-warn' : 'text-tx-secondary')}>{value}</b>
    </div>
  );
}
