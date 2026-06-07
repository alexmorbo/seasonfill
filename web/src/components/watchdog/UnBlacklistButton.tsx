import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, RotateCcw } from 'lucide-react';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter,
  DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { useUnBlacklist } from '@/lib/api/watchdogBlacklist';

export interface UnBlacklistButtonProps {
  instance: string;
  id: number;
  seriesTitle: string;
  seasonNumber: number;
}

// Mirrors CancelScanDialog: Dialog + destructive button. Trigger is a
// small ghost button matching design `.btn.sm`.
export function UnBlacklistButton({
  instance, id, seriesTitle, seasonNumber,
}: UnBlacklistButtonProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const m = useUnBlacklist();

  const handleConfirm = () => {
    m.mutate(
      { instance, id, seriesTitle, seasonNumber },
      { onSettled: () => setOpen(false) }, // hook owns toast + rollback
    );
  };

  return (
    <>
      <Button
        variant="outline" size="sm"
        onClick={() => setOpen(true)}
        disabled={m.isPending}
        data-testid={`un-blacklist-${id}`}
        className="h-7 gap-1 text-[12px]"
      >
        <RotateCcw className="h-3.5 w-3.5" />
        {t('watchdog.blacklist.unblacklistAction')}
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent data-testid={`un-blacklist-dialog-${id}`}>
          <DialogHeader>
            <DialogTitle>
              {t('watchdog.blacklist.unblacklistConfirm', {
                series: seriesTitle, season: seasonNumber,
              })}
            </DialogTitle>
            <DialogDescription>
              {t('watchdog.blacklist.unblacklistConfirmBody')}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost"
              onClick={() => setOpen(false)} disabled={m.isPending}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleConfirm}
              disabled={m.isPending}
              data-testid={`un-blacklist-confirm-${id}`}
            >
              {m.isPending && <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />}
              {t('watchdog.blacklist.unblacklistAction')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
