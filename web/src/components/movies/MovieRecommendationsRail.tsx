import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { toBcp47 } from '@/lib/locale';
import { Skeleton } from '@/components/ui/skeleton';
import { MovieCard } from './MovieCard';
import { useMovieRecommendations } from '@/api/movieRecommendations';

export interface MovieRecommendationsRailProps {
  readonly tmdbId: number;
  readonly limit?: number;
  readonly className?: string | undefined;
  // When true AND the fetched list is empty, render skeleton tiles + a loading
  // label instead of returning null. Parent passes this while the parent
  // /movies response degraded[] carries "tmdb_movie" (recs still warming).
  readonly loading?: boolean | undefined;
}

const TRACK = cn(
  'flex flex-row gap-3 overflow-x-auto snap-x snap-mandatory pb-2',
  'md:grid md:grid-cols-6 md:gap-4 md:overflow-visible md:snap-none md:pb-0',
);
const CARD = 'snap-start min-w-[124px] md:min-w-0';

// MovieRecommendationsRail — the movie analogue of RecommendationsCarousel.
// Reuses the MovieCard leaf (links to /movies/:tmdbId, poster/title/year/rating)
// — NOT RailCard (that is the SeriesDetail metadata sidebar). F-04: the movie
// recs DTO has no in_library / series_id / instance_name, so there is no
// library badge and no in-library click-through. Server rank order preserved.
export function MovieRecommendationsRail({
  tmdbId,
  limit = 20,
  className,
  loading,
}: MovieRecommendationsRailProps) {
  const { t, i18n } = useTranslation();
  const lang = toBcp47(i18n.resolvedLanguage);

  const query = useMovieRecommendations({
    tmdbId,
    limit,
    offset: 0,
    ...(lang ? { lang } : {}),
  });

  const items = query.data?.items ?? [];
  // Only tmdb-linkable items can render a card (F-04: each links to
  // /movies/:tmdb_id). filter+slice preserve the BE rank order — do NOT sort.
  const renderable = items
    .filter((it) => typeof it.tmdb_id === 'number' && (it.tmdb_id as number) > 0)
    .slice(0, limit);

  const heading = 'movie-recommendations-heading';
  const tmdbDegradedLocal = (query.data?.degraded ?? []).includes('tmdb_movie');
  const isLoading =
    query.isLoading ||
    (renderable.length === 0 && (tmdbDegradedLocal || Boolean(loading)));

  // A broken/errored rail must never break the detail page.
  if (query.isError) return null;
  // Empty + settled → render nothing (mirrors RecommendationsCarousel).
  if (renderable.length === 0 && !isLoading) return null;

  const showingSkeletons = renderable.length === 0;

  return (
    <section
      data-testid={showingSkeletons ? 'movie-recommendations-loading' : 'movie-recommendations'}
      aria-labelledby={heading}
      data-tmdb-id={tmdbId}
      className={cn('flex flex-col gap-3', className)}
    >
      <h2
        id={heading}
        className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint"
      >
        {t('movieDetail.recommendations.label')}
        {showingSkeletons && (
          <span
            data-testid="movie-recommendations-loading-label"
            className="ml-2 text-[10px] font-normal normal-case tracking-normal text-tx-muted"
          >
            {t('movieDetail.recommendations.loading')}
          </span>
        )}
      </h2>
      <div className={TRACK} data-testid="movie-recommendations-track">
        {showingSkeletons
          ? Array.from({ length: 6 }).map((_, i) => (
              <div
                key={i}
                data-testid="movie-recommendations-skeleton-tile"
                className={cn(CARD, 'flex flex-col gap-1.5')}
              >
                <Skeleton className="aspect-[2/3] w-full rounded-md" />
                <Skeleton className="h-3 w-[80%]" />
                <Skeleton className="h-2.5 w-[50%]" />
              </div>
            ))
          : renderable.map((rec, idx) => (
              <MovieCard
                key={`${rec.tmdb_id}-${idx}`}
                tmdbId={rec.tmdb_id as number}
                title={rec.title ?? ''}
                className={CARD}
                {...(rec.year !== undefined ? { year: rec.year } : {})}
                {...(rec.tmdb_rating !== undefined ? { rating: rec.tmdb_rating } : {})}
                {...(rec.poster_asset !== undefined ? { poster: rec.poster_asset } : {})}
              />
            ))}
      </div>
    </section>
  );
}
