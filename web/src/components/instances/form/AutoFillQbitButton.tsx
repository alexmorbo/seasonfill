import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { Loader2, Wand2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useDiscoverQbit } from '@/api/qbit';

export interface AutoFillQbitButtonProps {
  readonly instanceName: string;
  readonly onDiscovered: (
    fields: { url?: string; username?: string; category?: string },
  ) => void;
  readonly disabled?: boolean;
}

export function AutoFillQbitButton({
  instanceName, onDiscovered, disabled,
}: AutoFillQbitButtonProps) {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState(false);
  const q = useDiscoverQbit(instanceName, { enabled });

  useEffect(() => {
    if (q.isSuccess && q.data) {
      const next: { url?: string; username?: string; category?: string } = {};
      if (q.data.url) next.url = q.data.url;
      if (q.data.username !== undefined) next.username = q.data.username;
      if (q.data.category) next.category = q.data.category;
      onDiscovered(next);
      toast.success(t('settings.instances.form.watchdog.actions.autoFillSuccess'));
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEnabled(false);
    }
  }, [q.isSuccess, q.data, onDiscovered, t]);

  useEffect(() => {
    if (q.isError && q.error) {
      const err = q.error;
      const code =
        typeof err.body === 'object' && err.body !== null && 'code' in err.body
          ? (err.body as { code?: string }).code
          : '';
      if (err.status === 404 || code === 'NO_QBIT_FOUND') {
        toast.error(t('settings.instances.form.watchdog.actions.autoFillNoQbit'));
      } else {
        toast.error(t('settings.instances.form.watchdog.actions.autoFillFailed'));
      }
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setEnabled(false);
    }
  }, [q.isError, q.error, t]);

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="self-start gap-1.5"
      onClick={() => setEnabled(true)}
      disabled={Boolean(disabled) || q.isFetching}
      data-testid="auto-fill-qbit"
    >
      {q.isFetching
        ? <Loader2 className="w-3.5 h-3.5 animate-spin" />
        : <Wand2 className="w-3.5 h-3.5" />}
      {t('settings.instances.form.watchdog.actions.autoFill')}
    </Button>
  );
}
