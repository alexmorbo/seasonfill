import { useTranslation } from 'react-i18next';
import { AlertTriangle, CheckCircle2, Circle } from 'lucide-react';
import { Skeleton } from '@/components/ui/skeleton';
import { useWebhookStatus } from '@/api/qbit';

export interface WebhookStatusBadgeProps {
  readonly name: string;
}

// Cap error length so the badge never wraps; full text kept in `title`.
const ERROR_TRUNCATE = 50;

function truncate(s: string): string {
  if (s.length <= ERROR_TRUNCATE) return s;
  return `${s.slice(0, ERROR_TRUNCATE - 1)}…`;
}

// Surfaces live reconciler state. Backed by `useWebhookStatus`
// (10s-stale, refetch-on-focus). Error wins over installed=true.
export function WebhookStatusBadge({ name }: WebhookStatusBadgeProps) {
  const { t } = useTranslation();
  const q = useWebhookStatus(name);

  if (q.isPending) {
    return (
      <Skeleton
        data-testid="webhook-status-badge-loading"
        className="h-6 w-40"
      />
    );
  }

  const data = q.data;
  const err = data?.error?.trim() ?? '';
  const installed = Boolean(data?.installed);

  if (err) {
    const msg = t('settings.instances.form.webhookBadge.error', {
      message: truncate(err),
    });
    return (
      <span
        role="status"
        data-testid="webhook-status-badge"
        data-state="error"
        title={err}
        className="inline-flex items-center gap-1.5 rounded-md border border-status-warning/40 bg-status-warning/10 px-2 py-0.5 text-[11.5px] font-medium text-status-warning"
      >
        <AlertTriangle className="h-3 w-3" aria-hidden="true" />
        {msg}
      </span>
    );
  }

  if (installed) {
    return (
      <span
        role="status"
        data-testid="webhook-status-badge"
        data-state="installed"
        className="inline-flex items-center gap-1.5 rounded-md border border-status-success/40 bg-status-success/10 px-2 py-0.5 text-[11.5px] font-medium text-status-success"
      >
        <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
        {t('settings.instances.form.webhookBadge.installed')}
      </span>
    );
  }

  return (
    <span
      role="status"
      data-testid="webhook-status-badge"
      data-state="not-installed"
      className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2 py-0.5 text-[11.5px] font-medium text-muted"
    >
      <Circle className="h-3 w-3" aria-hidden="true" />
      {t('settings.instances.form.webhookBadge.notInstalled')}
    </span>
  );
}
