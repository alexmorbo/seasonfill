import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { Loader2 } from 'lucide-react';

export interface DirtyFooterProps {
  readonly mode: 'create' | 'edit';
  readonly isDirty: boolean;
  readonly isSubmitting: boolean;
  readonly editBlocked: boolean;
  readonly onCancel: () => void;
  readonly onSubmit: () => void;
}

export function DirtyFooter({
  mode, isDirty, isSubmitting, editBlocked, onCancel, onSubmit,
}: DirtyFooterProps) {
  const { t } = useTranslation();
  const isEdit = mode === 'edit';
  return (
    <div
      data-testid="dirty-footer"
      className="flex items-center gap-3 px-5 py-3.5 border-t border-border-faint bg-base"
    >
      {isEdit && isDirty && (
        <span
          data-testid="dirty-indicator"
          className="inline-flex items-center gap-1.5 text-[12.5px] text-status-warning"
        >
          <span className="w-[7px] h-[7px] rounded-full bg-status-warning" aria-hidden="true" />
          {t('settings.instances.form.footer.dirty')}
        </span>
      )}
      {!isEdit && (
        <span
          data-testid="create-webhook-hint"
          className="text-[12.5px] text-tx-muted"
        >
          {t('settings.instances.form.footer.createWebhookHint')}
        </span>
      )}
      <span className="flex-1" />
      <Button type="button" variant="ghost" onClick={onCancel}>
        {t('settings.instances.form.cancel')}
      </Button>
      <Button type="button" onClick={onSubmit} disabled={isSubmitting || editBlocked} data-testid="dirty-footer-save">
        {isSubmitting && <Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" />}
        {isSubmitting
          ? t('settings.instances.form.saving')
          : isEdit
            ? t('settings.instances.form.save')
            : t('settings.instances.form.footer.addInstance')}
      </Button>
    </div>
  );
}
