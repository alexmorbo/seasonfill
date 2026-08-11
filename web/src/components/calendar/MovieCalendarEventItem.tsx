import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import type { MovieCalendarEvent } from '@/api/movieCalendar';
import { MediaImage } from '@/components/MediaImage';
import { cn } from '@/lib/utils';
import { movieMilestoneMeta, movieEventTestId } from './movie-milestone';

// MovieCalendarEventItem — agenda-row analogue of CalendarEventItem for movie
// events. Links to /movies/:tmdbId, renders a milestone icon + label and the
// (raw-path) poster via MediaImage, exactly like the TV row does with a hash.
export function MovieCalendarEventItem({ event }: { event: MovieCalendarEvent }) {
  const { t } = useTranslation();
  const ms = movieMilestoneMeta(event.milestone);
  const tmdbId = event.tmdb_id;
  const title = event.title || (typeof tmdbId === 'number' ? `#${tmdbId}` : '—');

  return (
    <div
      className="flex items-center gap-3 rounded-md border border-border-faint bg-bg-base/40 px-3 py-2"
      data-testid={movieEventTestId(event)}
    >
      <div className="h-14 w-[38px] shrink-0 overflow-hidden rounded">
        <MediaImage
          hash={event.poster}
          kind="poster"
          title={title}
          fallback="monogram"
          className="w-full rounded"
        />
      </div>

      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {ms ? (
            <span
              className={cn('flex items-center gap-1 text-[12px] font-semibold', ms.className)}
              data-testid={`calendar-movie-milestone-${event.milestone}`}
            >
              <ms.Icon className="size-3.5" />
              {t(ms.labelKey)}
            </span>
          ) : null}
        </div>

        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {typeof tmdbId === 'number' ? (
            <Link
              to={`/movies/${tmdbId}`}
              className="truncate text-[13px] font-medium text-tx-secondary hover:text-tx-primary hover:underline"
            >
              {title}
            </Link>
          ) : (
            <span className="truncate text-[13px] font-medium text-tx-secondary">{title}</span>
          )}
        </div>
      </div>
    </div>
  );
}
