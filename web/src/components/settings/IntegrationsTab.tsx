import { useTranslation } from 'react-i18next';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { Link } from 'react-router-dom';
import { AlertTriangle, CheckCircle2, RefreshCw, Loader2, Server } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import { api, ApiError } from '@/lib/api';
import { cn } from '@/lib/utils';
import {
  useWebhookStatusAggregate,
  type WebhookStatusItem,
} from '@/lib/api/webhookStatus';
import { useRuntimeConfig } from '@/lib/runtime-config';

function Block({
  title, subtitle, headerRight, children,
}: {
  title: string;
  subtitle?: string;
  headerRight?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3.5">
      <header className="flex flex-col gap-[3px]">
        <div className="flex items-center gap-2">
          <h2 className="text-[15px] font-[650] tracking-[-0.01em] m-0">{title}</h2>
          {headerRight}
        </div>
        {subtitle && <p className="text-[12.5px] text-muted m-0">{subtitle}</p>}
      </header>
      {children}
    </section>
  );
}

function StatusPill({ item }: { item: WebhookStatusItem }) {
  const { t } = useTranslation();
  if (item.healthy && item.installed) {
    return (
      <span
        data-status="ok"
        className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-status-success bg-status-success-dim"
      >
        <CheckCircle2 className="w-3 h-3" aria-hidden="true" />
        {t('settings.integrations.webhooks.installed')}
      </span>
    );
  }
  if (!item.healthy) {
    return (
      <span
        data-status="error"
        title={item.error ?? undefined}
        className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-status-danger bg-status-danger-dim"
      >
        <AlertTriangle className="w-3 h-3" aria-hidden="true" />
        {t('settings.integrations.webhooks.error')}
      </span>
    );
  }
  return (
    <span
      data-status="missing"
      className="inline-flex items-center gap-1.5 px-2.5 h-[20px] rounded-full font-mono text-[11.5px] font-semibold text-tx-faint bg-bg-surface-2"
    >
      <span className="w-1.5 h-1.5 rounded-full bg-tx-faint" aria-hidden="true" />
      {t('settings.integrations.webhooks.missing')}
    </span>
  );
}

function WebhookRow({ item }: { item: WebhookStatusItem }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const reinstall = useMutation({
    mutationFn: async () => {
      return api(`/instances/${encodeURIComponent(item.instance_name)}/webhook/install`, {
        method: 'POST',
      });
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['webhook-status'] });
      toast.success(t('settings.integrations.webhooks.reinstallOk', { name: item.instance_name }));
    },
    onError: (err: unknown) => {
      const msg = err instanceof ApiError ? err.message : String(err);
      toast.error(t('settings.integrations.webhooks.reinstallErr', { name: item.instance_name, err: msg }));
    },
  });

  return (
    <div
      data-testid="integrations-webhook-row"
      data-instance={item.instance_name}
      className="flex items-center gap-3 py-3 border-b border-border-faint last:border-b-0"
    >
      <div className="flex flex-col gap-0.5 min-w-0 flex-1">
        <b className="text-[13.5px] font-[550]">{item.instance_name}</b>
        <span
          className={cn(
            'font-mono text-[12px] truncate',
            item.healthy ? 'text-tx-muted' : 'text-status-danger',
          )}
        >
          {item.error ?? item.url ?? `…/webhook/sonarr/${item.instance_name}`}
        </span>
      </div>
      <StatusPill item={item} />
      {!item.healthy && (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => reinstall.mutate()}
          disabled={reinstall.isPending}
          data-testid="integrations-webhook-reinstall"
        >
          {reinstall.isPending ? (
            <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />
          ) : (
            <RefreshCw className="w-3.5 h-3.5 mr-1.5" />
          )}
          {t('settings.integrations.webhooks.reinstall')}
        </Button>
      )}
    </div>
  );
}

function QbitDisabledField({
  id, label, testId,
}: {
  id: string;
  label: string;
  testId: string;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id} className="text-[12.5px] text-tx-secondary font-medium">
        {label}
      </Label>
      <Input
        id={id}
        data-testid={testId}
        disabled
        aria-disabled="true"
        value=""
        readOnly
        className="font-mono"
      />
    </div>
  );
}

function readQbitDefaults(runtime: ReturnType<typeof useRuntimeConfig>['data']) {
  if (!runtime?.config) return null;
  const raw = runtime.config as unknown as {
    qbit_defaults?: {
      category?: string;
      poll_interval_minutes?: number;
      regrab_cooldown_hours?: number;
      max_consecutive_no_better?: number;
    };
  };
  return raw.qbit_defaults ?? null;
}

export function IntegrationsTab() {
  const { t } = useTranslation();
  const agg = useWebhookStatusAggregate();
  const runtime = useRuntimeConfig();
  const qbitDefaults = readQbitDefaults(runtime.data);

  return (
    <div data-testid="integrations-tab" className="flex flex-col gap-5">
      <Block
        title={t('settings.integrations.webhooks.section')}
        subtitle={t('settings.integrations.webhooks.subtitle')}
      >
        {agg.isPending && (
          <>
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </>
        )}

        {agg.isError && (
          <Alert variant="destructive">
            <AlertTriangle className="w-4 h-4" />
            <AlertTitle>{t('settings.integrations.webhooks.loadFailed')}</AlertTitle>
            <AlertDescription>{agg.error.message}</AlertDescription>
          </Alert>
        )}

        {agg.data && agg.data.items.length === 0 && (
          <div className="flex items-center gap-3 py-4 text-[13px] text-muted">
            <Server className="w-4 h-4" aria-hidden="true" />
            {t('settings.integrations.webhooks.noInstances')}
            {' '}
            <Link to="/instances" className="underline text-tx-primary">
              {t('settings.integrations.webhooks.noInstancesLink')}
            </Link>
          </div>
        )}

        {agg.data && agg.data.items.length > 0 && (
          <div className="flex flex-col">
            {agg.data.items.map((it) => (
              <WebhookRow key={it.instance_name} item={it} />
            ))}
          </div>
        )}
      </Block>

      <Block
        title={t('settings.integrations.qbit.section')}
        subtitle={t('settings.integrations.qbit.subtitle')}
        headerRight={
          <Badge
            variant="neutral"
            data-testid="integrations-qbit-unavailable-badge"
            className="text-[10.5px] uppercase tracking-wider"
          >
            {t('settings.integrations.qbit.notYetAvailable')}
          </Badge>
        }
      >
        <div className="grid grid-cols-2 gap-3.5">
          <QbitDisabledField
            id="qbit-default-category"
            testId="qbit-default-category"
            label={t('settings.integrations.qbit.category')}
          />
          <QbitDisabledField
            id="qbit-default-poll-interval"
            testId="qbit-default-poll-interval"
            label={t('settings.integrations.qbit.pollInterval')}
          />
          <QbitDisabledField
            id="qbit-default-regrab-cooldown"
            testId="qbit-default-regrab-cooldown"
            label={t('settings.integrations.qbit.regrabCooldown')}
          />
          <QbitDisabledField
            id="qbit-default-max-no-better"
            testId="qbit-default-max-no-better"
            label={t('settings.integrations.qbit.maxNoBetter')}
          />
        </div>

        {!qbitDefaults && (
          <p className="text-[12px] text-tx-faint m-0">
            {t('settings.integrations.qbit.perInstanceNote')}
          </p>
        )}

        <p className="text-[12px] text-muted m-0">
          {t('settings.integrations.qbit.deepLinkPrefix')}{' '}
          <Link to="/instances" className="underline text-tx-primary">
            {t('settings.integrations.qbit.deepLinkLabel')}
          </Link>
          {t('settings.integrations.qbit.deepLinkSuffix')}
        </p>
      </Block>
    </div>
  );
}
