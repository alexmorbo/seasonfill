import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Pause, Play, RefreshCw } from 'lucide-react';
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import { useTorrentAction, type TorrentActionKind } from '@/lib/torrent-mutations';

export interface TorrentActionsProps {
  readonly instance: string;
  readonly hash: string;
  readonly health?: string | undefined;
  readonly className?: string | undefined;
}

// Map the DTO health bucket → Badge variant. Unknown/undefined renders
// nothing (no badge) rather than a misleading neutral chip.
const HEALTH_VARIANT: Record<string, 'ok' | 'warn' | 'danger'> = {
  ok: 'ok',
  stalled: 'warn',
  error: 'danger',
};

// Which actions require a confirm modal. Pause + recheck touch ratio/seed
// state on private trackers, so they gate behind a dialog; resume is
// always safe and fires immediately.
type ConfirmKind = 'pause' | 'recheck';

export function TorrentActions({ instance, hash, health, className }: TorrentActionsProps) {
  const { t } = useTranslation();
  const mutation = useTorrentAction();
  // `pending` = which confirm dialog is open (null = closed). Only pause
  // and recheck ever set this; resume bypasses it.
  const [pending, setPending] = useState<ConfirmKind | null>(null);

  const busy = mutation.isPending;

  const fire = (action: TorrentActionKind) => {
    mutation.mutate({ instance, hash, action });
  };

  const confirm = () => {
    if (!pending) return;
    mutation.mutate(
      { instance, hash, action: pending },
      { onSettled: () => setPending(null) },
    );
  };

  const healthVariant = health ? HEALTH_VARIANT[health] : undefined;

  return (
    <div className={cn('flex items-center justify-end gap-1.5', className)}>
      {healthVariant && (
        <Badge
          variant={healthVariant}
          data-testid="torrent-health"
          data-health={health}
        >
          {t(`seriesDetail.torrents.health.${health}`)}
        </Badge>
      )}

      <Button
        type="button"
        size="icon-btn"
        variant="ghost"
        onClick={() => setPending('pause')}
        disabled={busy}
        data-testid="torrent-action-pause"
        aria-label={t('seriesDetail.torrents.actions.pause')}
        title={t('seriesDetail.torrents.actions.pause')}
      >
        <Pause className="w-3.5 h-3.5" aria-hidden="true" />
      </Button>

      <Button
        type="button"
        size="icon-btn"
        variant="ghost"
        onClick={() => fire('resume')}
        disabled={busy}
        data-testid="torrent-action-resume"
        aria-label={t('seriesDetail.torrents.actions.resume')}
        title={t('seriesDetail.torrents.actions.resume')}
      >
        <Play className="w-3.5 h-3.5" aria-hidden="true" />
      </Button>

      <Button
        type="button"
        size="icon-btn"
        variant="ghost"
        onClick={() => setPending('recheck')}
        disabled={busy}
        data-testid="torrent-action-recheck"
        aria-label={t('seriesDetail.torrents.actions.recheck')}
        title={t('seriesDetail.torrents.actions.recheck')}
      >
        <RefreshCw className="w-3.5 h-3.5" aria-hidden="true" />
      </Button>

      <Dialog open={pending !== null} onOpenChange={(o) => { if (!o) setPending(null); }}>
        <DialogContent data-testid="torrent-confirm-dialog">
          {pending && (
            <>
              <DialogHeader>
                <DialogTitle>
                  {t(`seriesDetail.torrents.confirm.${pending}.title`)}
                </DialogTitle>
                <DialogDescription>
                  {t(`seriesDetail.torrents.confirm.${pending}.body`)}
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button
                  variant="ghost"
                  onClick={() => setPending(null)}
                  disabled={busy}
                >
                  {t(`seriesDetail.torrents.confirm.${pending}.keep`)}
                </Button>
                <Button
                  variant="destructive"
                  onClick={confirm}
                  disabled={busy}
                  data-testid="torrent-confirm-submit"
                >
                  {busy && <Loader2 className="w-3.5 h-3.5 mr-1 animate-spin" />}
                  {t(`seriesDetail.torrents.confirm.${pending}.confirm`)}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
