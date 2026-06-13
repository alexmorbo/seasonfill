import { useTranslation } from 'react-i18next';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';

export interface TrailerModalProps {
  readonly open: boolean;
  readonly onOpenChange: (next: boolean) => void;
  readonly youtubeKey: string;
  readonly name?: string | undefined;
}

export function TrailerModal({
  open,
  onOpenChange,
  youtubeKey,
  name,
}: TrailerModalProps) {
  const { t } = useTranslation();
  const title = name && name.trim().length > 0
    ? name
    : t('seriesDetail.trailer.fallbackTitle');

  // Build the privacy-respecting embed URL. `rel=0` keeps Google
  // from showing third-party "up next" videos at the end; `autoplay=1`
  // is permitted because the user just clicked the Trailer button.
  const src = `https://www.youtube-nocookie.com/embed/${encodeURIComponent(youtubeKey)}?autoplay=1&rel=0`;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        data-testid="trailer-modal"
        className={cn('max-w-3xl border-border-faint bg-bg-surface p-0 overflow-hidden')}
      >
        <DialogHeader className="px-4 pt-4 pb-2">
          <DialogTitle data-testid="trailer-modal-title" className="text-[14px] font-semibold text-tx-primary">
            {title}
          </DialogTitle>
        </DialogHeader>
        <div className="aspect-video w-full bg-black">
          {/* Mount-on-open, unmount-on-close: this is the only reliable
              way to stop the audio when the user hits Escape. */}
          {open && (
            <iframe
              data-testid="trailer-modal-iframe"
              src={src}
              title={title}
              width="100%"
              height="100%"
              className="w-full h-full"
              frameBorder="0"
              allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
              allowFullScreen
            />
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
