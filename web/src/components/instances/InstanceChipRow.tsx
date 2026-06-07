import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { List, Shield, Webhook } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import type { WebhookStatus } from '@/lib/webhook-status';
import { webhookHealthy } from '@/lib/webhook-status';
import type { QbitSettings } from '@/lib/qbit-settings';

export interface InstanceChipRowProps {
  readonly instanceName: string;
  readonly missingCount: number | undefined;
  readonly qbitSettings: QbitSettings | undefined;
  readonly webhookStatus: WebhookStatus | undefined;
}

/**
 * Hero chip row: missing-count (links to queue), watchdog state,
 * webhook health. Future B5 (story 049) will add re-grab/week and
 * blacklist chips here; the row already lays them out left-to-right
 * via flex-wrap.
 */
export function InstanceChipRow({
  instanceName, missingCount, qbitSettings, webhookStatus,
}: InstanceChipRowProps) {
  const { t } = useTranslation();
  const watchdogRunning = qbitSettings?.enabled === true;
  const webhookOk = webhookHealthy(webhookStatus);

  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="instance-chip-row">
      {typeof missingCount === 'number' && (
        <Link
          to={`/instances/${encodeURIComponent(instanceName)}/queue`}
          aria-label={t('instances.hero.chips.missingAria', { count: missingCount })}
          data-testid="chip-missing"
        >
          <Badge variant="solid" mono className="cursor-pointer hover:border-accent hover:text-accent">
            <List className="w-3 h-3 mr-1" />
            {t('instances.hero.chips.missing', { count: missingCount })}
          </Badge>
        </Link>
      )}
      {qbitSettings !== undefined && (
        <Badge variant="solid" mono data-testid="chip-watchdog">
          <Shield className="w-3 h-3 mr-1" />
          {watchdogRunning
            ? t('instances.hero.chips.watchdog.running')
            : t('instances.hero.chips.watchdog.stopped')}
        </Badge>
      )}
      {webhookStatus !== undefined && (
        <Badge
          variant={webhookOk ? 'ok' : 'warn'}
          mono
          data-testid="chip-webhook"
        >
          <Webhook className="w-3 h-3 mr-1" />
          {webhookOk
            ? t('instances.hero.chips.webhook.ok')
            : t('instances.hero.chips.webhook.error')}
        </Badge>
      )}
      {/* TODO(049): add re-grab/week + blacklist chips when /watchdog/rollups ships. */}
    </div>
  );
}
