import { Star, Trophy, ShieldCheck } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useMovieRatings } from '@/api/movieRatings';
import { humanizeVotes } from '@/components/series-detail/RatingDuo';

export interface MovieRatingsSectionProps {
  readonly tmdbId: number | undefined;
  readonly className?: string | undefined;
}

// Collapses OMDb's absent-value sentinels (nil / "" / "N/A") to empty — an
// absent awards / rated value is simply not rendered.
function isEmptyText(v: string | undefined): boolean {
  if (!v) return true;
  const trimmed = v.trim();
  return trimmed.length === 0 || trimmed.toUpperCase() === 'N/A';
}

function scoreValid(n: number | undefined): n is number {
  return typeof n === 'number' && n > 0;
}

// Ф3.3 — movie-detail ratings surface backed by GET /movies/:tmdb_id/ratings.
// Mirrors the series RatingsSection presentation but consumes useMovieRatings
// (NOT useSeriesRatings) and the movieDetail.ratings.* i18n namespace. Renders
// ONLY sources that carry a value; an unavailable/empty source is not rendered.
// `rated` is OMDb content-rating — DISTINCT from any TMDB content_rating badge.
export function MovieRatingsSection({ tmdbId, className }: MovieRatingsSectionProps) {
  const { t } = useTranslation();
  const { data } = useMovieRatings({ tmdbId });

  const showTmdb = scoreValid(data?.tmdb_rating);
  const showImdb = scoreValid(data?.imdb_rating);
  const rated = data?.rated;
  const showRated = !isEmptyText(rated);
  const awards = data?.awards;
  const showAwards = !isEmptyText(awards);

  if (!showTmdb && !showImdb && !showRated && !showAwards) return null;

  return (
    <section
      data-testid="movie-ratings-section"
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
        className,
      )}
    >
      <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
        {t('movieDetail.ratings.sectionTitle')}
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-[12.5px]">
        {showTmdb && (
          <span data-testid="movie-ratings-tmdb" className="inline-flex items-center gap-1.5">
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('movieDetail.ratings.tmdb')}
            </span>
            <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
            <span className="font-semibold text-tx-primary tabular-nums">{data!.tmdb_rating!.toFixed(1)}</span>
            {scoreValid(data?.tmdb_votes) && (
              <span
                className="text-tx-faint tabular-nums"
                aria-label={`${humanizeVotes(data!.tmdb_votes)} ${t('movieDetail.ratings.votes')}`}
              >
                · {humanizeVotes(data!.tmdb_votes)}
              </span>
            )}
          </span>
        )}

        {showImdb && (
          <span data-testid="movie-ratings-imdb" className="inline-flex items-center gap-1.5">
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('movieDetail.ratings.imdb')}
            </span>
            <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
            <span className="font-semibold text-tx-primary tabular-nums">{data!.imdb_rating!.toFixed(1)}</span>
            {scoreValid(data?.imdb_votes) && (
              <span
                className="text-tx-faint tabular-nums"
                aria-label={`${humanizeVotes(data!.imdb_votes)} ${t('movieDetail.ratings.votes')}`}
              >
                · {humanizeVotes(data!.imdb_votes)}
              </span>
            )}
          </span>
        )}

        {showRated && (
          // OMDb content-rating (`rated`, e.g. "PG-13") — a DISTINCT source
          // from any TMDB content_rating badge. Do NOT merge the two.
          <span data-testid="movie-ratings-rated" className="inline-flex items-center gap-1.5">
            <ShieldCheck className="w-3.5 h-3.5 text-tx-faint" aria-hidden="true" />
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('movieDetail.ratings.rated')}
            </span>
            <span className="font-semibold text-tx-primary">{rated}</span>
          </span>
        )}
      </div>

      {showAwards && (
        <div data-testid="movie-ratings-awards" className="flex items-start gap-2 text-[12.5px] text-tx-secondary">
          <Trophy className="w-3.5 h-3.5 mt-0.5 shrink-0 text-warn" aria-hidden="true" />
          <span>
            <span className="mr-1.5 text-[10px] font-bold uppercase tracking-wide text-tx-faint">
              {t('movieDetail.ratings.awards')}
            </span>
            {awards}
          </span>
        </div>
      )}
    </section>
  );
}
