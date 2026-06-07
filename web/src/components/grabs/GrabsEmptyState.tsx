import { useTranslation } from 'react-i18next';
import { Download, Play, List, X } from 'lucide-react';
import { Button } from '@/components/ui/button';

export interface GrabsEmptyStateProps {
  variant: 'top' | 'fails' | 'search';
  onScan?: () => void;
  onQueue?: (() => void) | undefined;
  queueCount?: number | undefined;
  onClearSearch?: () => void;
}

export function GrabsEmptyState({
  variant, onScan, onQueue, queueCount, onClearSearch,
}: GrabsEmptyStateProps) {
  const { t } = useTranslation();
  return (
    <div className="max-w-[520px] mx-auto mt-6 rounded-xl border border-border-faint bg-bg-surface p-8 text-center flex flex-col items-center gap-3">
      <div className="size-12 rounded-full bg-bg-surface-2 grid place-items-center text-tx-muted">
        {variant === 'search' ? <X className="size-5" /> : <Download className="size-5" />}
      </div>
      <h2 className="text-[18px] font-semibold tracking-tight">
        {t(`grabs.empty.${variant}.title`)}
      </h2>
      <p className="text-tx-muted text-[13px] max-w-[420px]">
        {t(`grabs.empty.${variant}.body`)}
      </p>
      <div className="flex gap-2 mt-2">
        {variant === 'top' && (
          <>
            {onScan && (
              <Button variant="primary" size="sm" onClick={onScan} className="gap-1.5">
                <Play className="size-3.5" />
                {t('grabs.empty.cta.scan')}
              </Button>
            )}
            {onQueue && (
              <Button variant="outline" size="sm" onClick={onQueue} className="gap-1.5">
                <List className="size-3.5" />
                {t('grabs.empty.cta.queue', { count: queueCount ?? 0 })}
              </Button>
            )}
          </>
        )}
        {variant === 'search' && onClearSearch && (
          <Button variant="outline" size="sm" onClick={onClearSearch}>
            {t('grabs.empty.cta.clearSearch')}
          </Button>
        )}
      </div>
    </div>
  );
}
