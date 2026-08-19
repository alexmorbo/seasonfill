import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useFormatDate } from '@/lib/timezone';
import { StaleBadge } from '@/components/series-detail/StaleBadge';

export interface MovieSyncFooterProps {
  // ISO timestamp of the last successful enrichment sync. Absent → the whole
  // footer renders nothing. NOTE: dto.MovieDetailResponse does not yet expose
  // synced_at (S4 shipped the 4 money/identity fields only); the movie page
  // reads it forward-compatibly, so this footer lights up the moment BE adds
  // the column to the assembler.
  readonly syncedAt?: string | undefined;
  readonly tmdbStale?: boolean | undefined;
  readonly omdbStale?: boolean | undefined;
  readonly className?: string | undefined;
}

// Movie analogue of the SeriesDetail synced/stale footer (SeriesDetail.tsx
// ~418-424). FORKED into a component (the series version is inlined on the
// page) so the movie page composes the same "Synced {time}" line + per-source
// StaleBadge. Reuses the shared StaleBadge + seriesDetail.synced /
// seriesDetail.stale.* i18n keys.
export function MovieSyncFooter({
  syncedAt,
  tmdbStale,
  omdbStale,
  className,
}: MovieSyncFooterProps) {
  const { t } = useTranslation();
  const fmt = useFormatDate();
  if (!syncedAt) return null;
  return (
    <div
      data-testid="movie-sync-footer"
      className={cn(
        'flex items-center justify-end gap-2 text-[11px] text-tx-faint pt-1',
        className,
      )}
    >
      <span>{t('seriesDetail.synced', { time: fmt(syncedAt, 'datetime') })}</span>
      {tmdbStale && <StaleBadge asOf={syncedAt} source="tmdb" />}
      {omdbStale && <StaleBadge asOf={syncedAt} source="omdb" />}
    </div>
  );
}
