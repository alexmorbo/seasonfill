import { Inbox, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface TorrentsEmptyStateProps {
  readonly variant: 'never' | 'all-deleted';
  readonly className?: string | undefined;
}

export function TorrentsEmptyState({ variant, className }: TorrentsEmptyStateProps) {
  const { t } = useTranslation();
  const Icon = variant === 'never' ? Inbox : Trash2;
  return (
    <div
      data-testid="torrents-empty"
      data-variant={variant}
      className={cn(
        'flex flex-col items-center justify-center gap-1.5 py-8 text-center',
        'text-tx-muted',
        className,
      )}
    >
      <Icon className="w-5 h-5 text-tx-faint" aria-hidden="true" />
      <div className="text-[13px] font-medium text-tx-secondary">
        {t(`seriesDetail.torrents.empty.${variant}.title`)}
      </div>
      <div className="text-[11.5px] text-tx-muted max-w-xs">
        {t(`seriesDetail.torrents.empty.${variant}.body`)}
      </div>
    </div>
  );
}
