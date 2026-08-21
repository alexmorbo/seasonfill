import { useMutation } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2, Wand2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { ApiError, api } from '@/lib/api';
import type { QbitDiscoverDTO } from '@/api/qbit';

export interface AutoFillFields {
  url?: string;
  username?: string;
  category?: string;
}

export interface AutoFillApplyResult {
  readonly changed: boolean;
}

export interface AutoFillQbitButtonProps {
  readonly instanceName: string;
  /**
   * Short display name of the arr this instance is ("Sonarr" | "Radarr"),
   * interpolated as {{arr}} into the button label and both toasts. The caller
   * computes it (WatchdogSection useWatch('type') + t('instances.type.*')) —
   * same caller-computes-label pattern as WebhookSubCard. ADR-0023 F2.
   */
  readonly arrLabel: string;
  readonly onApply: (fields: AutoFillFields) => AutoFillApplyResult;
  readonly disabled?: boolean;
}

function errorCode(err: ApiError): string {
  if (typeof err.body === 'object' && err.body !== null && 'code' in err.body) {
    const c = (err.body as { code: unknown }).code;
    return typeof c === 'string' ? c : '';
  }
  return '';
}

export function AutoFillQbitButton({
  instanceName, arrLabel, onApply, disabled,
}: AutoFillQbitButtonProps) {
  const { t } = useTranslation();

  const m = useMutation<QbitDiscoverDTO, ApiError, void>({
    mutationFn: () =>
      api<QbitDiscoverDTO>(
        `/instances/${encodeURIComponent(instanceName)}/discover/qbit`,
      ),
    onSuccess: (data) => {
      const fields: AutoFillFields = {};
      if (data.url) fields.url = data.url;
      if (data.username !== undefined) fields.username = data.username;
      if (data.category) fields.category = data.category;
      const result = onApply(fields);
      if (result.changed) {
        toast.success(t('settings.instances.form.watchdog.actions.autoFillSuccess', { arr: arrLabel }));
      }
    },
    onError: (err) => {
      const code = errorCode(err);
      if (err.status === 404 || code === 'NO_QBIT_FOUND') {
        toast.error(t('settings.instances.form.watchdog.actions.autoFillNoQbit', { arr: arrLabel }));
      } else {
        toast.error(t('settings.instances.form.watchdog.actions.autoFillFailed'));
      }
    },
  });

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="self-start gap-1.5"
      onClick={() => m.mutate()}
      disabled={Boolean(disabled) || m.isPending}
      data-testid="auto-fill-qbit"
    >
      {m.isPending
        ? <Loader2 className="w-3.5 h-3.5 animate-spin" />
        : <Wand2 className="w-3.5 h-3.5" />}
      {t('settings.instances.form.watchdog.actions.autoFill', { arr: arrLabel })}
    </Button>
  );
}
