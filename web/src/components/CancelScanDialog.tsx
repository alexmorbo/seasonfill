import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { useCancelScan } from '@/lib/scan-mutations';

// Dedicated component (not inline in ScanDetail) because the destructive
// confirm modal needs a stable test surface. "CancelScanDialog" naming
// keeps future shadcn AlertDialog adoption a mechanical swap.
export function CancelScanDialog({ scanId, disabled }: {
  scanId: string;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const cancel = useCancelScan();

  const handleConfirm = () => {
    cancel.mutate({ id: scanId }, {
      // Close the modal on either outcome — useCancelScan owns toasts.
      onSettled: () => setOpen(false),
    });
  };

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        disabled={disabled || cancel.isPending}
        data-testid="cancel-scan-button"
        className="h-7 text-[12px] border-status-warning/50 text-status-warning hover:bg-status-warning/10"
      >
        {t('scans.actions.cancel')}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent data-testid="cancel-scan-dialog">
          <DialogHeader>
            <DialogTitle>{t('scans.cancelDialog.title')}</DialogTitle>
            <DialogDescription>
              {t('scans.cancelDialog.body')}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={cancel.isPending}>
              {t('scans.cancelDialog.keepRunning')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleConfirm}
              disabled={cancel.isPending}
              data-testid="cancel-scan-confirm"
            >
              {cancel.isPending && <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />}
              {t('scans.cancelDialog.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
