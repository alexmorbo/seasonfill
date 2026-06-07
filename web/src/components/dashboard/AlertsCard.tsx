import { CircleAlert, WifiOff, Check, TriangleAlert } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import { Badge } from '@/components/ui/badge';
import { useWebhookStatusAggregate, type WebhookStatusItem } from '@/lib/api/webhookStatus';
import { useInstances, type Instance } from '@/lib/instances';
import { cn } from '@/lib/utils';

type AlertRow = { key: string; severity: 'danger' | 'warn'; title: string; body: string; rawError?: string };
type Copy = { webhookTitle: (i: string) => string; webhookBody: string; instanceTitle: (n: string) => string; instanceBodyFallback: string };

// Danger before warn so the eye triages the worst first.
function buildRows(webhooks: WebhookStatusItem[] | undefined, instances: readonly Instance[] | undefined, c: Copy): AlertRow[] {
  const out: AlertRow[] = [];
  for (const w of webhooks ?? []) {
    if (w.healthy) continue;
    out.push({ key: `wh:${w.instance_name}`, severity: 'danger', title: c.webhookTitle(w.instance_name), body: c.webhookBody, ...(w.error ? { rawError: w.error } : {}) });
  }
  for (const i of instances ?? []) {
    const h = (i.health as string | undefined ?? '').toLowerCase();
    if (h === 'available' || h === '') continue;
    out.push({ key: `inst:${i.name ?? ''}`, severity: 'warn', title: c.instanceTitle(i.name ?? ''), body: i.last_error ?? c.instanceBodyFallback, ...(i.last_error ? { rawError: i.last_error } : {}) });
  }
  return out;
}

export function AlertsCard() {
  const { t } = useTranslation();
  const whQ = useWebhookStatusAggregate();
  const instQ = useInstances();
  const bothFailed = whQ.isError && instQ.isError;

  if (whQ.isPending || instQ.isPending) {
    return (
      <Card>
        <CardHeader className="flex flex-row items-baseline justify-between p-4 pb-2">
          <CardTitle className="text-sm font-semibold">{t('dashboard.rail.alerts.title')}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 p-3 pt-0"><Skeleton className="h-12 w-full" /></CardContent>
      </Card>
    );
  }

  const rows = buildRows(whQ.data?.items, instQ.data?.instances, {
    webhookTitle: (i) => t('dashboard.rail.alerts.webhookError.title', { instance: i }),
    webhookBody: t('dashboard.rail.alerts.webhookError.body'),
    instanceTitle: (n) => t('dashboard.rail.alerts.instanceDown.title', { name: n }),
    instanceBodyFallback: t('dashboard.rail.alerts.instanceDown.bodyFallback'),
  });

  return (
    <Card data-testid="alerts-card">
      <CardHeader className="flex flex-row items-baseline justify-between p-4 pb-2">
        <CardTitle className="flex items-center gap-2 text-sm font-semibold">
          {t('dashboard.rail.alerts.title')}
          {bothFailed && <TriangleAlert className="h-3.5 w-3.5 text-warn" aria-label={t('dashboard.rail.loadFailed')} data-testid="alerts-load-failed" />}
        </CardTitle>
        {rows.length > 0 && <Badge variant="destructive" className="font-mono" data-testid="alerts-count">{rows.length}</Badge>}
      </CardHeader>
      <CardContent className="flex flex-col gap-2 p-3 pt-0">
        {rows.length === 0 ? (
          <div className="flex items-center gap-2 p-3 text-sm text-ok" data-testid="alerts-allclear">
            <Check className="h-4 w-4" /><span>{t('dashboard.rail.alerts.allclear')}</span>
          </div>
        ) : rows.map((row) => (
          <div key={row.key} data-severity={row.severity} data-testid={`alert-row-${row.key}`} title={row.rawError}
               className={cn('flex items-start gap-2 rounded-md border p-2.5',
                 row.severity === 'danger' ? 'border-danger/30 bg-danger-dim' : 'border-warn/30 bg-warn-dim')}>
            <span className={cn('mt-px', row.severity === 'danger' ? 'text-danger' : 'text-warn')}>
              {row.severity === 'danger' ? <CircleAlert className="h-4 w-4" /> : <WifiOff className="h-4 w-4" />}
            </span>
            <span className="flex-1 min-w-0">
              <b className="block text-sm font-semibold">{row.title}</b>
              <span className="text-xs text-tx-muted">{row.body}</span>
            </span>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
