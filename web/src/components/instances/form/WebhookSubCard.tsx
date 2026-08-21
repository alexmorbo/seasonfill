import { useWatch, type Control } from 'react-hook-form';
import { useTranslation } from 'react-i18next';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { WebhookStatusBadge } from '@/components/settings/WebhookStatusBadge';
import { cn } from '@/lib/utils';

export interface WebhookSubCardProps {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly control: Control<any, any, any>;
  readonly mode: 'create' | 'edit';
  readonly instanceName: string | undefined;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  readonly register: any;
  readonly errorOverride?: string;
}

export function WebhookSubCard({
  control, mode, instanceName, register, errorOverride,
}: WebhookSubCardProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  const arrType = (useWatch({
    control,
    name: 'type',
  }) ?? 'sonarr') as 'sonarr' | 'radarr';
  const arrLabel = t(`instances.type.${arrType === 'radarr' ? 'radarr' : 'sonarr'}`);
  return (
    <div
      data-testid="webhook-subcard"
      className={cn(
        'bg-base border border-border-faint rounded-md p-[13px]',
        'flex flex-col gap-[11px]',
      )}
    >
      <div className="flex items-center gap-2.5">
        <span className="text-[12.5px] font-semibold flex-1">
          {t('settings.instances.form.connection.webhookTitle', { arr: arrLabel })}
        </span>
        {isEdit && instanceName ? (
          <WebhookStatusBadge name={instanceName} />
        ) : (
          <span
            data-testid="webhook-create-pill"
            className="font-mono text-[10.5px] text-tx-faint bg-surface-2 border border-border-faint px-2 py-[1px] rounded-md"
          >
            {t('settings.instances.form.connection.webhookCreatePill')}
          </span>
        )}
      </div>

      {(
        <div className="flex flex-col gap-1.5">
          <Label
            htmlFor="inst-webhook-url-override"
            className="text-[10.5px] font-semibold uppercase tracking-[0.08em] text-tx-faint"
          >
            {t('settings.instances.form.connection.webhookOverrideLabel')}
          </Label>
          <Input
            id="inst-webhook-url-override"
            type="url"
            className="font-mono"
            placeholder={t('settings.instances.form.connection.webhookOverridePlaceholder')}
            aria-invalid={Boolean(errorOverride) || undefined}
            {...register('webhook_url_override')}
          />
          <p className="text-[11.5px] text-tx-muted">
            {t('settings.instances.form.connection.webhookOverrideHelp')}
          </p>
          {errorOverride && (
            <p role="alert" className="text-status-danger text-[11.5px]">
              {errorOverride}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
