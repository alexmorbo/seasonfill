import { Star, Trophy, ShieldCheck } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { useSeriesRatings } from '@/api/seriesRatings';
import { humanizeVotes } from './RatingDuo';

export interface RatingsSectionProps {
  readonly seriesId: number | undefined;
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

// W18-7b — canonical detail-page ratings surface backed by the SWR /ratings
// endpoint. Renders ONLY sources that carry a value; an unavailable/empty
// source is simply not rendered. `rated` is OMDb content-rating (F-07),
// DISTINCT from the TMDB `content_rating` badge rendered in the hero.
export function RatingsSection({ seriesId, className }: RatingsSectionProps) {
  const { t } = useTranslation();
  const { data } = useSeriesRatings({ seriesId });

  const showTmdb = scoreValid(data?.tmdb_rating);
  const showImdb = scoreValid(data?.imdb_rating);
  const rated = data?.rated;
  const showRated = !isEmptyText(rated);
  const awards = data?.awards;
  const showAwards = !isEmptyText(awards);

  if (!showTmdb && !showImdb && !showRated && !showAwards) return null;

  return (
    <section
      data-testid="ratings-section"
      className={cn(
        'flex flex-col gap-3 rounded-lg border border-border-faint bg-bg-surface/60 px-4 py-3',
        className,
      )}
    >
      <div className="flex items-center gap-2 text-[10.5px] font-bold uppercase tracking-wide text-tx-faint">
        {t('seriesDetail.ratings.sectionTitle')}
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-[12.5px]">
        {showTmdb && (
          <span data-testid="ratings-tmdb" className="inline-flex items-center gap-1.5">
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('seriesDetail.ratings.tmdb')}
            </span>
            <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
            <span className="font-semibold text-tx-primary tabular-nums">{data!.tmdb_rating!.toFixed(1)}</span>
            {scoreValid(data?.tmdb_votes) && (
              <span
                className="text-tx-faint tabular-nums"
                aria-label={`${humanizeVotes(data!.tmdb_votes)} ${t('seriesDetail.ratings.votes')}`}
              >
                · {humanizeVotes(data!.tmdb_votes)}
              </span>
            )}
          </span>
        )}

        {showImdb && (
          <span data-testid="ratings-imdb" className="inline-flex items-center gap-1.5">
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('seriesDetail.ratings.imdb')}
            </span>
            <Star className="w-3.5 h-3.5 text-warn" aria-hidden="true" fill="currentColor" />
            <span className="font-semibold text-tx-primary tabular-nums">{data!.imdb_rating!.toFixed(1)}</span>
            {scoreValid(data?.imdb_votes) && (
              <span
                className="text-tx-faint tabular-nums"
                aria-label={`${humanizeVotes(data!.imdb_votes)} ${t('seriesDetail.ratings.votes')}`}
              >
                · {humanizeVotes(data!.imdb_votes)}
              </span>
            )}
          </span>
        )}

        {showRated && (
          // F-07: OMDb content-rating (`rated`, e.g. "TV-MA") — a DISTINCT
          // source from the TMDB `content_rating` badge shown in the hero. Do
          // NOT reuse ContentRatingBadge / merge the two.
          <span data-testid="ratings-rated" className="inline-flex items-center gap-1.5">
            <ShieldCheck className="w-3.5 h-3.5 text-tx-faint" aria-hidden="true" />
            <span className="text-[10px] font-bold tracking-wide uppercase text-tx-faint">
              {t('seriesDetail.ratings.rated')}
            </span>
            <span className="font-semibold text-tx-primary">{rated}</span>
          </span>
        )}
      </div>

      {showAwards && (
        <div data-testid="ratings-awards" className="flex items-start gap-2 text-[12.5px] text-tx-secondary">
          <Trophy className="w-3.5 h-3.5 mt-0.5 shrink-0 text-warn" aria-hidden="true" />
          <span>
            <span className="mr-1.5 text-[10px] font-bold uppercase tracking-wide text-tx-faint">
              {t('seriesDetail.ratings.awards')}
            </span>
            {awards}
          </span>
        </div>
      )}
    </section>
  );
}
