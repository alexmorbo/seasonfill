import { Inbox, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';

export interface TorrentsEmptyStateProps {
  readonly variant: 'never' | 'all-deleted';
  readonly className?: string | undefined;
  /**
   * i18n key-path prefix for the copy. Defaults to 'seriesDetail.torrents'
   * — unchanged behavior for the existing (series) call site. B1.5
   * (ADR-0023) — MovieTorrentsSection passes 'movieDetail.torrents' so the
   * "never" empty state reads "movie"/"Radarr" instead of "series"/"Sonarr".
   * Only the 'never' variant currently ships movie-flavored copy — the
   * 'all-deleted' variant is unreachable today (TorrentsSection /
   * MovieTorrentsSection both handle the all-deleted case with their own
   * inline note, not this component), so no
   * `movieDetail.torrents.empty['all-deleted']` keys were added.
   */
  readonly i18nBase?: string | undefined;
}

export function TorrentsEmptyState({ variant, className, i18nBase = 'seriesDetail.torrents' }: TorrentsEmptyStateProps) {
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
        {t(`${i18nBase}.empty.${variant}.title`)}
      </div>
      <div className="text-[11.5px] text-tx-muted max-w-xs">
        {t(`${i18nBase}.empty.${variant}.body`)}
      </div>
    </div>
  );
}
