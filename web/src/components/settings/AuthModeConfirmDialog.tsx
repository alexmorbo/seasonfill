import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ShieldAlert, Check } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import type { AuthMode } from './AuthModeSegmented';

interface Props {
  readonly open: boolean;
  readonly onOpenChange: (next: boolean) => void;
  readonly currentMode: AuthMode;
  readonly targetMode: AuthMode | null;
  readonly onConfirm: () => void;
}

const LABEL: Record<AuthMode, string> = {
  forms: 'Forms', basic: 'Basic', none: 'None', oidc: 'OIDC',
};

export function AuthModeConfirmDialog({
  open, onOpenChange, currentMode, targetMode, onConfirm,
}: Props) {
  const { t } = useTranslation();
  const [ack, setAck] = useState(false);

  // Reset ack every time the dialog opens/closes — the next attempt
  // must start unchecked to keep the danger affordance honest.
  useEffect(() => {
    if (!open) setAck(false);
  }, [open]);

  const handleConfirm = () => {
    if (!ack || !targetMode) return;
    onConfirm();
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        data-testid="auth-mode-confirm-dialog"
        className="max-w-[460px] p-0 overflow-hidden border border-border-subtle"
      >
        <div className="flex gap-3 items-start p-5 pb-1">
          <div className="w-[38px] h-[38px] rounded-[10px] flex items-center justify-center bg-status-danger-dim text-status-danger shrink-0">
            <ShieldAlert className="w-[19px] h-[19px]" aria-hidden="true" />
          </div>
          <DialogHeader className="space-y-1">
            <DialogTitle className="text-[16px] font-[650]">
              {t('settings.modeConfirm.title', {
                target: targetMode ? LABEL[targetMode] : '',
              })}
            </DialogTitle>
            <DialogDescription className="text-[13px] text-muted leading-relaxed">
              {t('settings.modeConfirm.body', {
                current: LABEL[currentMode],
                target: targetMode ? LABEL[targetMode] : '',
              })}
            </DialogDescription>
          </DialogHeader>
        </div>

        <div className="px-5 py-3.5">
          <button
            type="button"
            data-testid="auth-mode-confirm-ack"
            onClick={() => setAck((v) => !v)}
            aria-pressed={ack}
            className={cn(
              'flex items-center gap-2.5 w-full text-[13px] text-tx-secondary',
              'bg-bg-base border border-border-faint rounded-[var(--r-md)] px-3 py-2.5',
              'hover:border-border-subtle text-left cursor-pointer',
            )}
          >
            <span
              className={cn(
                'w-[17px] h-[17px] rounded-[4px] border-[1.5px] flex items-center justify-center shrink-0',
                ack ? 'bg-accent border-transparent text-accent-foreground' : 'border-border-strong',
              )}
            >
              <Check className={cn('w-3 h-3', ack ? 'opacity-100' : 'opacity-0')} />
            </span>
            {t('settings.modeConfirm.ack')}
          </button>
        </div>

        <DialogFooter className="px-5 py-3.5 border-t border-border-faint bg-bg-base flex justify-end gap-2.5">
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            data-testid="auth-mode-confirm-cancel"
          >
            {t('settings.modeConfirm.cancel')}
          </Button>
          <Button
            type="button"
            variant="destructive"
            disabled={!ack || !targetMode}
            onClick={handleConfirm}
            data-testid="auth-mode-confirm-confirm"
          >
            {t('settings.modeConfirm.confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
