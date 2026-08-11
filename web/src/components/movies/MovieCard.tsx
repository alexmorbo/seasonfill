import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { Star } from 'lucide-react';
import { cn } from '@/lib/utils';
import { formatSeriesTitle } from '@/lib/title';
import { MediaImage } from '@/components/MediaImage';

export interface MovieCardProps {
  readonly title: string;
  readonly year?: number | undefined;
  /** ★ shown only when a positive number. Absent/0 → year alone, no star. */
  readonly rating?: number | undefined;
  /** RAW canon poster_asset path → rendered via the /api/v1/media/{path}
   *  handler (mediaUrl), identical to the movie detail poster. */
  readonly poster?: string | null | undefined;
  /** TMDB id → direct link to /movies/:tmdbId (movies are keyed by tmdb_id;
   *  no canon-id resolution step). */
  readonly tmdbId: number;
  /** "In library" badge (top-left). */
  readonly libraryBadge?: boolean | undefined;
  readonly className?: string | undefined;
}

// MovieCard — portrait tile for the movie library grid. Mirrors SeriesCard's
// corner-overlay markup (year bottom-left, ★ rating bottom-right, in-library
// badge top-left) but links straight to /movies/:tmdbId. The poster is a raw
// asset path, so it's rendered through MediaImage (same /api/v1/media/{path}
// URL as the detail poster) with a monogram fallback.
export function MovieCard({
  title,
  year,
  rating,
  poster,
  tmdbId,
  libraryBadge,
  className,
}: MovieCardProps) {
  const { t } = useTranslation();

  const showRating = typeof rating === 'number' && rating > 0;
  const showYear = year !== undefined;
  const ariaLabel = t('movieCard.open', {
    title: formatSeriesTitle(title, year),
  });

  return (
    <Link
      to={`/movies/${tmdbId}`}
      data-testid="movie-card"
      data-tmdb-id={tmdbId}
      aria-label={ariaLabel}
      className={cn(
        'group relative block',
        'transition-transform duration-150 ease-out hover:-translate-y-0.5',
        'focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-accent rounded-lg',
        className,
      )}
    >
      <div className="relative aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-bg-surface-2">
        <MediaImage
          hash={poster ?? null}
          kind="poster"
          title={title}
          fallback="monogram"
          aspectRatio="aspect-auto"
          className="absolute inset-0"
          data-testid="movie-card-poster"
        />

        {libraryBadge && (
          <span
            data-testid="movie-card-library-badge"
            className="absolute left-2 top-2 z-20 inline-flex items-center rounded-full bg-accent/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-accent backdrop-blur-sm"
          >
            {t('discovery.in_library')}
          </span>
        )}

        {(showYear || showRating) && (
          <div
            aria-hidden="true"
            className="pointer-events-none absolute inset-x-0 bottom-0 z-10 h-14 bg-gradient-to-t from-black/70 to-transparent"
          />
        )}

        {showYear && (
          <span
            data-testid="movie-card-year"
            className="absolute bottom-2 left-2 z-20 inline-flex items-center rounded-md bg-black/60 px-1.5 py-0.5 text-[10.5px] font-semibold tabular-nums text-white backdrop-blur-sm"
          >
            {year}
          </span>
        )}

        {showRating && (
          <span
            data-testid="movie-card-rating"
            className="absolute bottom-2 right-2 z-20 inline-flex items-center gap-0.5 rounded-md bg-black/60 px-1.5 py-0.5 text-[10.5px] font-semibold tabular-nums text-white backdrop-blur-sm"
          >
            <Star
              className="h-2.5 w-2.5 text-warn"
              aria-hidden="true"
              fill="currentColor"
            />
            {(rating as number).toFixed(1)}
          </span>
        )}
      </div>

      <div className="flex flex-col gap-1 px-0.5 pt-2">
        <div
          data-testid="movie-card-title"
          className="truncate text-[13px] font-semibold text-tx-primary"
          title={title}
        >
          {title}
        </div>
      </div>
    </Link>
  );
}
